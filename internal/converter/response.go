package converter

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"claude-code-proxy-go/internal/model"
)

// ConvertOpenAIToClaudeResponse converts an OpenAI response dict to Claude message format.
func ConvertOpenAIToClaudeResponse(openaiResponse map[string]any, originalRequest *model.MessagesRequest) map[string]any {
	// Extract choices[0].message
	choices, _ := openaiResponse["choices"].([]any)
	if len(choices) == 0 {
		return errorResponse("no choices in OpenAI response")
	}

	choice, _ := choices[0].(map[string]any)
	if choice == nil {
		return errorResponse("invalid choice format")
	}

	message, _ := choice["message"].(map[string]any)
	if message == nil {
		return errorResponse("no message in choice")
	}

	// Build content blocks
	var contentBlocks []map[string]any

	// Text content
	if text, ok := message["content"].(string); ok && text != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": text,
		})
	}

	// Tool calls
	if toolCalls, ok := message["tool_calls"].([]any); ok {
		for _, tc := range toolCalls {
			tcMap, ok := tc.(map[string]any)
			if !ok {
				continue
			}
			funcInfo, _ := tcMap["function"].(map[string]any)
			if funcInfo == nil {
				continue
			}

			// Parse arguments JSON
			var input any
			if args, ok := funcInfo["arguments"].(string); ok {
				if err := json.Unmarshal([]byte(args), &input); err != nil {
					slog.Warn("failed to parse tool call arguments", "error", err, "args", args)
					input = map[string]any{}
				}
			}

			contentBlocks = append(contentBlocks, map[string]any{
				"type":  "tool_use",
				"id":    tcMap["id"],
				"name":  funcInfo["name"],
				"input": input,
			})
		}
	}

	// If no content blocks, add empty text
	if len(contentBlocks) == 0 {
		contentBlocks = append(contentBlocks, map[string]any{
			"type": "text",
			"text": "",
		})
	}

	// Map finish_reason to stop_reason
	finishReason, _ := choice["finish_reason"].(string)
	stopReason := mapFinishReason(finishReason)

	// Extract usage
	usage := extractUsage(openaiResponse)

	// Get model name
	modelName, _ := openaiResponse["model"].(string)
	if modelName == "" && originalRequest != nil {
		modelName = originalRequest.Model
	}

	// Get response ID
	responseID, _ := openaiResponse["id"].(string)

	return map[string]any{
		"id":            responseID,
		"type":          "message",
		"role":          "assistant",
		"model":         modelName,
		"content":       contentBlocks,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage":         usage,
	}
}

// ConvertOpenAIStreamingToClaude reads an OpenAI SSE stream and writes Claude SSE events.
func ConvertOpenAIStreamingToClaude(
	w http.ResponseWriter,
	openaiStream io.Reader,
	originalRequest *model.MessagesRequest,
	ctx context.Context,
	isDisconnected func() bool,
	cancelFn func(),
) {
	// Generate message ID
	messageID := generateMessageID()

	// Determine model name
	modelName := ""
	if originalRequest != nil {
		modelName = originalRequest.Model
	}

	// Write initial message_start event
	writeSSEEvent(w, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            messageID,
			"type":          "message",
			"role":          "assistant",
			"model":         modelName,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens": 0,
				"output_tokens": 0,
			},
		},
	})

	// Write content_block_start for text (index 0)
	writeSSEEvent(w, "content_block_start", map[string]any{
		"type":         "content_block_start",
		"index":        0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	})

	// Write ping event
	writeSSEEvent(w, "ping", map[string]any{
		"type": "ping",
	})

	// Track state
	type toolCallState struct {
		id        string
		name      string
		argsBuf   strings.Builder
		started   bool
	}

	var (
		contentBlockIndex   = 0
		textBlockStarted    = true
		toolCalls           = make(map[int]*toolCallState)
		currentTextBlock    = 0
		inputTokens         = 0
		outputTokens        = 0
		cachedTokens        = 0
		stopReason          = "end_turn"
		hasTextContent      = false
	)

	scanner := bufio.NewScanner(openaiStream)
	// Set a large buffer for potentially long SSE lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	for scanner.Scan() {
		// Check for disconnection
		if isDisconnected != nil && isDisconnected() {
			if cancelFn != nil {
				cancelFn()
			}
			return
		}

		line := scanner.Text()

		// Skip non-data lines
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		// Parse chunk JSON
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Debug("failed to parse SSE chunk", "error", err, "data", data)
			continue
		}

		// Track usage from chunk
		if usage, ok := chunk["usage"].(map[string]any); ok {
			if v, ok := usage["prompt_tokens"].(float64); ok {
				inputTokens = int(v)
			}
			if v, ok := usage["completion_tokens"].(float64); ok {
				outputTokens = int(v)
			}
			// Check prompt_tokens_details for cached tokens
			if details, ok := usage["prompt_tokens_details"].(map[string]any); ok {
				if v, ok := details["cached_tokens"].(float64); ok {
					cachedTokens = int(v)
				}
			}
		}

		// Extract choices
		choices, _ := chunk["choices"].([]any)
		if len(choices) == 0 {
			continue
		}

		choiceMap, _ := choices[0].(map[string]any)
		if choiceMap == nil {
			continue
		}

		delta, _ := choiceMap["delta"].(map[string]any)
		if delta == nil {
			continue
		}

		// Handle text content
		if content, ok := delta["content"].(string); ok && content != "" {
			hasTextContent = true
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": currentTextBlock,
				"delta": map[string]any{
					"type": "text_delta",
					"text": content,
				},
			})
		}

		// Handle tool calls
		if toolCallsData, ok := delta["tool_calls"].([]any); ok {
			for _, tc := range toolCallsData {
				tcMap, ok := tc.(map[string]any)
				if !ok {
					continue
				}

				idx := 0
				if v, ok := tcMap["index"].(float64); ok {
					idx = int(v)
				}

				state, exists := toolCalls[idx]
				if !exists {
					state = &toolCallState{}
					toolCalls[idx] = state
				}

				// Accumulate tool call info
				if id, ok := tcMap["id"].(string); ok && id != "" {
					state.id = id
				}

				if fn, ok := tcMap["function"].(map[string]any); ok {
					if name, ok := fn["name"].(string); ok && name != "" {
						state.name = name
					}
					if args, ok := fn["arguments"].(string); ok {
						state.argsBuf.WriteString(args)
					}
				}

				// Start tool call content block when we have id and name
				if !state.started && state.id != "" && state.name != "" {
					if textBlockStarted && !hasTextContent {
						// Close the initial text block since we're switching to tool calls
						writeSSEEvent(w, "content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": currentTextBlock,
						})
						textBlockStarted = false
					} else if hasTextContent && contentBlockIndex == 0 {
						// Close text block
						writeSSEEvent(w, "content_block_stop", map[string]any{
							"type":  "content_block_stop",
							"index": currentTextBlock,
						})
						textBlockStarted = false
					}

					contentBlockIndex++
					writeSSEEvent(w, "content_block_start", map[string]any{
						"type":  "content_block_start",
						"index": contentBlockIndex,
						"content_block": map[string]any{
							"type":  "tool_use",
							"id":    state.id,
							"name":  state.name,
							"input": map[string]any{},
						},
					})
					state.started = true
				}

				// Emit argument deltas when args buffer has valid JSON
				if state.started && state.argsBuf.Len() > 0 {
					argsSoFar := state.argsBuf.String()
					// Try to parse - if it's valid JSON, we can emit delta
					var parsed any
					if json.Unmarshal([]byte(argsSoFar), &parsed) == nil {
						// Emit as input_json_delta
						writeSSEEvent(w, "content_block_delta", map[string]any{
							"type":  "content_block_delta",
							"index": contentBlockIndex,
							"delta": map[string]any{
								"type":          "input_json_delta",
								"partial_json": argsSoFar,
							},
						})
						// Reset buffer since we've emitted it
						state.argsBuf.Reset()
					}
				}
			}
		}

		// Handle finish_reason
		if reason, ok := choiceMap["finish_reason"].(string); ok && reason != "" {
			stopReason = mapFinishReason(reason)
		}
	}

	// Close the last open content block
	writeSSEEvent(w, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": contentBlockIndex,
	})

	// Write message_delta with stop_reason and usage
	usageMap := map[string]any{
		"output_tokens": outputTokens,
	}

	writeSSEEvent(w, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": usageMap,
	})

	// Write message_stop
	writeSSEEvent(w, "message_stop", map[string]any{
		"type": "message_stop",
	})

	// Flush
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	// Log final usage
	slog.Info("streaming complete",
		"input_tokens", inputTokens,
		"output_tokens", outputTokens,
		"cached_tokens", cachedTokens,
		"stop_reason", stopReason,
	)
}

// writeSSEEvent writes a Server-Sent Event to the response writer.
func writeSSEEvent(w http.ResponseWriter, event string, data any) {
	jsonData, err := json.Marshal(data)
	if err != nil {
		slog.Error("failed to marshal SSE event", "error", err, "event", event)
		return
	}

	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, string(jsonData))
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// mapFinishReason maps OpenAI finish_reason to Claude stop_reason.
func mapFinishReason(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter":
		return "end_turn"
	default:
		return "end_turn"
	}
}

// extractUsage extracts usage information from OpenAI response.
func extractUsage(response map[string]any) map[string]any {
	result := map[string]any{
		"input_tokens":  0,
		"output_tokens": 0,
	}

	usage, ok := response["usage"].(map[string]any)
	if !ok {
		return result
	}

	if v, ok := usage["prompt_tokens"].(float64); ok {
		result["input_tokens"] = int(v)
	}
	if v, ok := usage["completion_tokens"].(float64); ok {
		result["output_tokens"] = int(v)
	}

	return result
}

// generateMessageID generates a random message ID in Claude format.
func generateMessageID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		// Fallback
		return "msg_00000000000000000000000000"
	}
	return "msg_" + hex.EncodeToString(b)
}

// errorResponse creates an error response in Claude format.
func errorResponse(message string) map[string]any {
	return map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": message,
		},
	}
}
