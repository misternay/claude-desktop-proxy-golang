package handler

import (
	"crypto/rand"
	"encoding/json"
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

// CreateMessage handles POST /v1/messages - converts Claude requests to OpenAI and proxies them.
func CreateMessage(w http.ResponseWriter, r *http.Request) {
	var req model.MessagesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": fmt.Sprintf("Invalid request body: %s", err.Error()),
			},
		})
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": fmt.Sprintf("Failed to create stream: %s", err.Error()),
				},
			})
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
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": fmt.Sprintf("Failed to create completion: %s", err.Error()),
				},
			})
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
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "invalid_request_error",
				"message": fmt.Sprintf("Invalid request body: %s", err.Error()),
			},
		})
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
	models := []map[string]any{
		{
			"id":           "claude-3-5-sonnet-20241022",
			"display_name": "Claude 3.5 Sonnet",
			"created_at":   "2024-10-22T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-3-5-haiku-20241022",
			"display_name": "Claude 3.5 Haiku",
			"created_at":   "2024-10-22T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-3-opus-20240229",
			"display_name": "Claude 3 Opus",
			"created_at":   "2024-02-29T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-3-sonnet-20240229",
			"display_name": "Claude 3 Sonnet",
			"created_at":   "2024-02-29T00:00:00Z",
			"created_by":   "anthropic",
		},
		{
			"id":           "claude-3-haiku-20240307",
			"display_name": "Claude 3 Haiku",
			"created_at":   "2024-03-07T00:00:00Z",
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
			"openai_base_url":  config.AppConfig.OpenAIBaseURL,
			"big_model":        config.AppConfig.BigModel,
			"middle_model":     config.AppConfig.MiddleModel,
			"small_model":      config.AppConfig.SmallModel,
			"host":             config.AppConfig.Host,
			"port":             config.AppConfig.Port,
			"log_level":        config.AppConfig.LogLevel,
			"request_timeout":  config.AppConfig.RequestTimeout,
			"max_retries":      config.AppConfig.MaxRetries,
		},
		"endpoints": endpoints,
	})
}
