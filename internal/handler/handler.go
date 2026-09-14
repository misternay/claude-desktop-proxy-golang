package handler

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"claude-code-proxy-go/internal/client"
	"claude-code-proxy-go/internal/config"
	"claude-code-proxy-go/internal/converter"
	"claude-code-proxy-go/internal/model"
	"claude-code-proxy-go/internal/modelmanager"
)

// generateUUID generates a UUID v4 using crypto/rand (stdlib only).
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant 10
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

var (
	openAIClient *client.OpenAIClient
	modelMgr     *modelmanager.ModelManager
)

// InitHandlers initializes the global handler dependencies.
func InitHandlers() {
	customHeaders := config.AppConfig.GetCustomHeaders()
	openAIClient = client.NewOpenAIClient(
		config.AppConfig.RequestTimeout,
		config.AppConfig.AzureAPIVersion,
		customHeaders,
	)
	modelMgr = modelmanager.NewModelManager(config.AppConfig)
}

// maxRequestBodyBytes caps the accepted request body size. 64 MiB is large
// enough for base64-encoded screenshots while still bounding proxy memory.
const maxRequestBodyBytes = 64 << 20

// decodeJSONBody decodes the request body into v under a hard size limit.
// Decode failures respond in Claude error format: 413 when the body exceeded
// the limit (*http.MaxBytesError), 400 for any other malformed input.
// Returns false when the response has been written and the caller must return.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		status := http.StatusBadRequest
		errType := "invalid_request_error"
		msg := fmt.Sprintf("Invalid request body: %s", err.Error())
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			status = http.StatusRequestEntityTooLarge
			msg = fmt.Sprintf("request body too large: limit is %d bytes", maxRequestBodyBytes)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    errType,
				"message": msg,
			},
		})
		return false
	}
	return true
}

// writeUpstreamError responds to the client with the Claude error matching an
// upstream failure, preserving the upstream message text. *client.OpenAIError
// carries the upstream HTTP status and is mapped via ClaudeError; any other
// error (network failure, marshal failure) degrades to 502 api_error.
func writeUpstreamError(w http.ResponseWriter, action string, err error) {
	status := http.StatusBadGateway
	errType := "api_error"
	var openaiErr *client.OpenAIError
	if errors.As(err, &openaiErr) {
		status, errType = openaiErr.ClaudeError()
		// Forward upstream Retry-After on rate limits so clients can back off
		// for the exact duration requested.
		if retryAfter := openaiErr.Headers.Get("Retry-After"); retryAfter != "" {
			w.Header().Set("Retry-After", retryAfter)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    errType,
			"message": fmt.Sprintf("%s: %s", action, err.Error()),
		},
	})
}

// CreateMessage handles POST /v1/messages - converts Claude requests to OpenAI and proxies them.
func CreateMessage(w http.ResponseWriter, r *http.Request) {
	var req model.MessagesRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	requestID := generateUUID()

	// Get model configuration
	openaiModel, apiKey, baseURL := modelMgr.GetModelConfig(req.Model)
	_ = openaiModel // used internally by converter via modelMgr

	// Convert Claude request to OpenAI format
	openaiReq := converter.ConvertClaudeToOpenAI(&req, modelMgr)

	ctx := r.Context()

	if req.Stream {
		// Streaming response
		stream, err := openAIClient.CreateChatCompletionStream(ctx, openaiReq, apiKey, baseURL, requestID)
		if err != nil {
			writeUpstreamError(w, "Failed to create stream", err)
			return
		}
		defer stream.Close()

		// Set SSE headers
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Transfer-Encoding", "chunked")
		w.WriteHeader(http.StatusOK)

		// Flush headers
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}

		isDisconnected := func() bool {
			select {
			case <-r.Context().Done():
				return true
			default:
				return false
			}
		}

		cancelFn := func() {
			openAIClient.CancelRequest(requestID)
		}

		converter.ConvertOpenAIStreamingToClaude(w, stream, &req, ctx, isDisconnected, cancelFn)
	} else {
		// Non-streaming response
		openaiResp, err := openAIClient.CreateChatCompletion(ctx, openaiReq, apiKey, baseURL, requestID)
		if err != nil {
			writeUpstreamError(w, "Failed to create completion", err)
			return
		}

		claudeResp := converter.ConvertOpenAIToClaudeResponse(openaiResp, &req)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(claudeResp)
	}
}

// CountTokens handles POST /v1/messages/count_tokens - estimates token count.
func CountTokens(w http.ResponseWriter, r *http.Request) {
	var req model.TokenCountRequest
	if !decodeJSONBody(w, r, &req) {
		return
	}

	totalChars := 0

	// Count system prompt characters
	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			totalChars += len(s)
		case []any:
			for _, block := range s {
				if m, ok := block.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						totalChars += len(t)
					}
				}
			}
		}
	}

	// Count message content characters
	for _, msg := range req.Messages {
		if msg.Content == nil {
			continue
		}
		switch c := msg.Content.(type) {
		case string:
			totalChars += len(c)
		case []any:
			for _, block := range c {
				if m, ok := block.(map[string]any); ok {
					if t, ok := m["text"].(string); ok {
						totalChars += len(t)
					}
				}
			}
		}
	}

	// Estimate tokens: roughly 1 token per 4 characters
	estimated := totalChars / 4
	if estimated < 1 {
		estimated = 1
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"input_tokens": estimated,
	})
}

// ListModels handles GET /v1/models - returns available Claude models.
func ListModels(w http.ResponseWriter, r *http.Request) {
	// Current lineup as of Sep 2026. From the 4.6 generation on, Anthropic
	// uses dateless pinned-snapshot IDs; older generations keep dated IDs.
	models := []map[string]any{
		{
			"id":           "claude-fable-5-1",
			"display_name": "Claude Fable 5.1",
			"created_at":   "2026-09-01T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-opus-5",
			"display_name": "Claude Opus 5",
			"created_at":   "2026-07-24T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-sonnet-5",
			"display_name": "Claude Sonnet 5",
			"created_at":   "2026-06-30T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-haiku-4-5-20251001",
			"display_name": "Claude Haiku 4.5",
			"created_at":   "2025-10-01T00:00:00Z",
			"created_by":   "anthropic",
		},
		// Recent legacy generations — still available on the Anthropic API.
		{
			"id":           "claude-opus-4-8",
			"display_name": "Claude Opus 4.8",
			"created_at":   "2026-05-01T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-opus-4-7",
			"display_name": "Claude Opus 4.7",
			"created_at":   "2026-04-16T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-sonnet-4-6",
			"display_name": "Claude Sonnet 4.6",
			"created_at":   "2026-02-17T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-opus-4-6",
			"display_name": "Claude Opus 4.6",
			"created_at":   "2026-02-05T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-opus-4-5-20251101",
			"display_name": "Claude Opus 4.5",
			"created_at":   "2025-11-01T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-sonnet-4-5-20250929",
			"display_name": "Claude Sonnet 4.5",
			"created_at":   "2025-09-29T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-opus-4-1-20250805",
			"display_name": "Claude Opus 4.1",
			"created_at":   "2025-08-05T00:00:00Z",
			"created_by":   "anthropic",
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"data": models,
	})
}

// HealthCheck handles GET /health - returns proxy health status.
func HealthCheck(w http.ResponseWriter, r *http.Request) {
	apiKeyValid := config.AppConfig.OpenAIAPIKey != ""
	clientValidation := config.AppConfig.AnthropicAPIKey != ""

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":                    "healthy",
		"timestamp":                 time.Now().UTC().Format(time.RFC3339),
		"openai_api_configured":     apiKeyValid,
		"api_key_valid":             apiKeyValid,
		"client_api_key_validation": clientValidation,
	})
}

// TestConnection handles GET /test-connection - tests the OpenAI API connection.
func TestConnection(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID := generateUUID()

	_, apiKey, baseURL := modelMgr.GetModelConfig("small_model")

	testReq := map[string]any{
		"model": config.AppConfig.SmallModel,
		"messages": []map[string]any{
			{"role": "user", "content": "Hello"},
		},
		"max_tokens": 5,
	}

	openaiResp, err := openAIClient.CreateChatCompletion(ctx, testReq, apiKey, baseURL, requestID)
	if err != nil {
		log.Printf("Test connection failed: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"details": fmt.Sprintf("Connection failed: %s", err.Error()),
		})
		return
	}

	_ = openaiResp
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"success": true,
		"details": "Successfully connected to OpenAI API",
	})
}

// Root handles GET / - returns proxy information.
func Root(w http.ResponseWriter, r *http.Request) {
	endpoints := []map[string]string{
		{"method": "POST", "path": "/v1/messages", "description": "Create a message (Claude API compatible)"},
		{"method": "POST", "path": "/v1/messages/count_tokens", "description": "Count tokens in a message"},
		{"method": "GET", "path": "/v1/models", "description": "List available models"},
		{"method": "GET", "path": "/health", "description": "Health check endpoint"},
		{"method": "GET", "path": "/test-connection", "description": "Test OpenAI API connection"},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"name":    "Claude-to-OpenAI Proxy",
		"version": "1.0.0",
		"config": map[string]any{
			"openai_base_url": config.AppConfig.OpenAIBaseURL,
			"big_model":       config.AppConfig.BigModel,
			"middle_model":    config.AppConfig.MiddleModel,
			"small_model":     config.AppConfig.SmallModel,
			"host":            config.AppConfig.Host,
			"port":            config.AppConfig.Port,
			"log_level":       config.AppConfig.LogLevel,
			"request_timeout": config.AppConfig.RequestTimeout,
			"max_retries":     config.AppConfig.MaxRetries,
		},
		"endpoints": endpoints,
	})
}
