package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OpenAIClient is an HTTP client for the OpenAI API with request cancellation support.
type OpenAIClient struct {
	Timeout        time.Duration
	APIVersion     string // For Azure OpenAI
	CustomHeaders  map[string]string
	ActiveRequests map[string]context.CancelFunc
	mu             sync.RWMutex
}

// NewOpenAIClient creates a new OpenAI client.
func NewOpenAIClient(timeout int, apiVersion string, customHeaders map[string]string) *OpenAIClient {
	return &OpenAIClient{
		Timeout:        time.Duration(timeout) * time.Second,
		APIVersion:     apiVersion,
		CustomHeaders:  customHeaders,
		ActiveRequests: make(map[string]context.CancelFunc),
	}
}

// CreateChatCompletion sends a non-streaming chat completion request to OpenAI.
func (c *OpenAIClient) CreateChatCompletion(
	ctx context.Context,
	request map[string]any,
	apiKey string,
	baseURL string,
	requestID string,
) (map[string]any, error) {
	// Ensure streaming is disabled
	request["stream"] = false

	// Create context with cancellation for tracking
	ctx, cancel := context.WithCancel(ctx)
	c.trackRequest(requestID, cancel)
	defer c.untrackRequest(requestID)

	// Build request URL
	requestURL := buildURL(baseURL, "/chat/completions", c.APIVersion)

	// Marshal request body
	body, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	c.setHeaders(req, apiKey)

	// Send request
	httpClient := &http.Client{Timeout: c.Timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response JSON
	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response JSON: %w", err)
	}

	// Handle error status codes
	if resp.StatusCode != http.StatusOK {
		errorMsg := extractErrorMessage(result, resp.StatusCode)
		errorType := ClassifyOpenAIError(errorMsg)
		return nil, &OpenAIError{
			StatusCode: resp.StatusCode,
			Message:    errorMsg,
			Type:       errorType,
		}
	}

	return result, nil
}

// CreateChatCompletionStream sends a streaming chat completion request and returns the response body.
func (c *OpenAIClient) CreateChatCompletionStream(
	ctx context.Context,
	request map[string]any,
	apiKey string,
	baseURL string,
	requestID string,
) (io.ReadCloser, error) {
	// Enable streaming with usage tracking
	request["stream"] = true
	request["stream_options"] = map[string]any{
		"include_usage": true,
	}

	// Create context with cancellation for tracking
	ctx, cancel := context.WithCancel(ctx)
	c.trackRequest(requestID, cancel)

	// Build request URL
	requestURL := buildURL(baseURL, "/chat/completions", c.APIVersion)

	// Marshal request body
	body, err := json.Marshal(request)
	if err != nil {
		c.untrackRequest(requestID)
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(body))
	if err != nil {
		c.untrackRequest(requestID)
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	c.setHeaders(req, apiKey)
	req.Header.Set("Accept", "text/event-stream")

	// Send request
	httpClient := &http.Client{Timeout: c.Timeout}
	resp, err := httpClient.Do(req)
	if err != nil {
		c.untrackRequest(requestID)
		return nil, fmt.Errorf("request failed: %w", err)
	}

	// Handle error status codes
	if resp.StatusCode != http.StatusOK {
		defer c.untrackRequest(requestID)
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(resp.Body)
		var result map[string]any
		json.Unmarshal(respBody, &result)

		errorMsg := extractErrorMessage(result, resp.StatusCode)
		errorType := ClassifyOpenAIError(errorMsg)
		return nil, &OpenAIError{
			StatusCode: resp.StatusCode,
			Message:    errorMsg,
			Type:       errorType,
		}
	}

	// Return a wrapped ReadCloser that cleans up tracking on close
	return &trackedReadCloser{
		ReadCloser: resp.Body,
		onClose: func() {
			c.untrackRequest(requestID)
		},
	}, nil
}

// CancelRequest cancels an active request by its ID.
func (c *OpenAIClient) CancelRequest(requestID string) bool {
	c.mu.RLock()
	cancel, exists := c.ActiveRequests[requestID]
	c.mu.RUnlock()

	if exists {
		slog.Info("cancelling request", "request_id", requestID)
		cancel()
		return true
	}
	return false
}

// trackRequest registers a request's cancel function.
func (c *OpenAIClient) trackRequest(requestID string, cancel context.CancelFunc) {
	c.mu.Lock()
	c.ActiveRequests[requestID] = cancel
	c.mu.Unlock()
}

// untrackRequest removes a request's cancel function.
func (c *OpenAIClient) untrackRequest(requestID string) {
	c.mu.Lock()
	delete(c.ActiveRequests, requestID)
	c.mu.Unlock()
}

// setHeaders sets the common headers on an HTTP request.
func (c *OpenAIClient) setHeaders(req *http.Request, apiKey string) {
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "claude-proxy/1.0.0")

	// Add custom headers
	for key, value := range c.CustomHeaders {
		req.Header.Set(key, value)
	}
}

// buildURL constructs the full request URL with optional API version query param.
func buildURL(baseURL, path, apiVersion string) string {
	// Ensure baseURL doesn't end with slash
	baseURL = strings.TrimRight(baseURL, "/")
	fullURL := baseURL + path

	// Add API version for Azure
	if apiVersion != "" {
		u, err := url.Parse(fullURL)
		if err == nil {
			q := u.Query()
			q.Set("api-version", apiVersion)
			u.RawQuery = q.Encode()
			fullURL = u.String()
		}
	}

	return fullURL
}

// extractErrorMessage extracts an error message from an OpenAI error response.
func extractErrorMessage(response map[string]any, statusCode int) string {
	if errObj, ok := response["error"].(map[string]any); ok {
		if msg, ok := errObj["message"].(string); ok {
			return msg
		}
	}
	if msg, ok := response["message"].(string); ok {
		return msg
	}
	return fmt.Sprintf("OpenAI API error (status %d)", statusCode)
}

// OpenAIError represents an error from the OpenAI API.
type OpenAIError struct {
	StatusCode int
	Message    string
	Type       string
}

func (e *OpenAIError) Error() string {
	return fmt.Sprintf("OpenAI API error [%d, %s]: %s", e.StatusCode, e.Type, e.Message)
}

// ClassifyOpenAIError classifies common OpenAI error messages into categories.
func ClassifyOpenAIError(errorMsg string) string {
	lower := strings.ToLower(errorMsg)

	switch {
	case strings.Contains(lower, "invalid_api_key") || strings.Contains(lower, "invalid api key") ||
		strings.Contains(lower, "incorrect api key") || strings.Contains(lower, "unauthorized"):
		return "authentication"

	case strings.Contains(lower, "rate_limit") || strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "too many requests"):
		return "rate_limit"

	case strings.Contains(lower, "does not exist") || strings.Contains(lower, "model_not_found") ||
		strings.Contains(lower, "model not found"):
		return "model_not_found"

	case strings.Contains(lower, "invalid_request") || strings.Contains(lower, "bad request") ||
		strings.Contains(lower, "invalid request"):
		return "bad_request"

	case strings.Contains(lower, "unsupported_region") || strings.Contains(lower, "unsupported region") ||
		strings.Contains(lower, "not available in your region"):
		return "unsupported_region"

	case strings.Contains(lower, "billing") || strings.Contains(lower, "quota") ||
		strings.Contains(lower, "insufficient_quota"):
		return "billing"

	case strings.Contains(lower, "context_length") || strings.Contains(lower, "maximum context length") ||
		strings.Contains(lower, "too many tokens"):
		return "context_length"

	default:
		return "unknown"
	}
}

// trackedReadCloser wraps an io.ReadCloser with an onClose callback.
type trackedReadCloser struct {
	io.ReadCloser
	onClose func()
}

func (t *trackedReadCloser) Close() error {
	if t.onClose != nil {
		t.onClose()
	}
	return t.ReadCloser.Close()
}
