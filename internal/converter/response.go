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
	"time"

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

	// Reasoning content (DeepSeek/GLM-style thinking) becomes a thinking
	// block, placed first to match Claude's content block ordering.
	if rc := reasoningText(message); rc != "" {
		contentBlocks = append(contentBlocks, map[string]any{
			"type":     "thinking",
			"thinking": rc,
		})
	}

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

// StreamIdleTimeout is the longest the streaming converter waits between SSE
// lines before treating the upstream as stalled. A var so tests can shorten it.
var StreamIdleTimeout = 120 * time.Second

// reasoningText extracts thinking text from an OpenAI-style message or delta.
// DeepSeek/GLM-style upstreams expose it as reasoning_content; OpenRouter
// uses reasoning.
func reasoningText(m map[string]any) string {
	if rc, ok := m["reasoning_content"].(string); ok && rc != "" {
		return rc
	}
	if rc, ok := m["reasoning"].(string); ok && rc != "" {
		return rc
	}
	return ""
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
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})

	// Write ping event
	writeSSEEvent(w, "ping", map[string]any{
		"type": "ping",
	})

	// Track state
	type toolCallState struct {
		id      string
		name    string
		argsBuf strings.Builder
		started bool
	}

	var (
		contentBlockIndex = -1 // -1 = no content block emitted yet
		thinkingOpen      = false
		textOpen          = false
		textIndex         = 0
		toolCalls         = make(map[int]*toolCallState)
		inputTokens       = 0
		outputTokens      = 0
		cachedTokens      = 0
		stopReason        = "end_turn"
	)

	// Content blocks are emitted lazily so that thinking (reasoning_content)
	// precedes text and tool_use blocks, matching Claude's block ordering.
	closeOpenBlock := func() {
		if contentBlockIndex >= 0 {
			writeSSEEvent(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": contentBlockIndex,
			})
		}
	}
	openBlock := func(block map[string]any) int {
		closeOpenBlock()
		contentBlockIndex++
		writeSSEEvent(w, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         contentBlockIndex,
			"content_block": block,
		})
		return contentBlockIndex
	}

	scanner := bufio.NewScanner(openaiStream)
	// Set a large buffer for potentially long SSE lines
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

	// Pump lines through a channel so the main loop can also select on the
	// idle timer and the request context. readErr records a scanner failure
	// (e.g. upstream closed the body abruptly); EOF is not an error.
	lines := make(chan string, 64)
	readErr := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		if err := scanner.Err(); err != nil {
			readErr <- err
			return
		}
		close(lines)
	}()

	idleTimer := time.NewTimer(StreamIdleTimeout)
	defer idleTimer.Stop()

	// abort is set when the stream terminates abnormally (in-stream error
	// frame); the caller then skips the normal message_delta/message_stop
	// epilogue. Declared before processLine so the closure can capture it.
	abort := false

	// processLine handles one raw SSE line. It is a closure over the block
	// state so the main loop stays a tight select. It returns false when the
	// stream should terminate early ([DONE], or an in-stream error frame);
	// an error frame also sets abort so the caller skips the normal
	// message_delta/message_stop epilogue.
	processLine := func(line string) bool {
		// Skip non-data lines. Some upstreams (e.g. Huawei ModelArts via
		// bifrost) emit "data:" without the spec's optional trailing space;
		// accept both forms.
		if !strings.HasPrefix(line, "data:") {
			return true
		}

		data := strings.TrimPrefix(line, "data:")
		data = strings.TrimPrefix(data, " ")
		if data == "[DONE]" {
			return false
		}

		// Parse chunk JSON
		var chunk map[string]any
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			slog.Debug("failed to parse SSE chunk", "error", err, "data", data)
			return true
		}

		// Surface in-stream error frames. Strict upstreams can return HTTP 200
		// and then deliver the error as an SSE data frame (e.g. ModelArts
		// validation errors); silently skipping them would give the client an
		// empty response.
		if errObj, ok := chunk["error"].(map[string]any); ok {
			errMsg := fmt.Sprintf("%v", errObj["message"])
			if errMsg == "" || errMsg == "<nil>" {
				if b, err := json.Marshal(errObj); err == nil {
					errMsg = string(b)
				}
			}
			slog.Error("upstream reported in-stream error", "error", errMsg)
			writeSSEEvent(w, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": errMsg,
				},
			})
			abort = true
			return false
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
			return true
		}

		choiceMap, _ := choices[0].(map[string]any)
		if choiceMap == nil {
			return true
		}

		delta, _ := choiceMap["delta"].(map[string]any)
		if delta == nil {
			return true
		}

		// Handle reasoning content (thinking) — emitted as a thinking block
		// ahead of any text or tool_use blocks.
		if rc := reasoningText(delta); rc != "" {
			if !thinkingOpen {
				thinkingOpen = true
				textOpen = false
				openBlock(map[string]any{"type": "thinking", "thinking": ""})
			}
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": contentBlockIndex,
				"delta": map[string]any{
					"type":     "thinking_delta",
					"thinking": rc,
				},
			})
		}

		// Handle text content
		if content, ok := delta["content"].(string); ok && content != "" {
			if !textOpen {
				thinkingOpen = false
				textOpen = true
				textIndex = openBlock(map[string]any{"type": "text", "text": ""})
			}
			writeSSEEvent(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textIndex,
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
					// openBlock closes whatever is currently open (thinking,
					// text, or the previous tool_use) before starting this one.
					thinkingOpen = false
					textOpen = false
					openBlock(map[string]any{
						"type":  "tool_use",
						"id":    state.id,
						"name":  state.name,
						"input": map[string]any{},
					})
					state.started = true
				}

				// Emit argument fragments immediately as partial_json deltas.
				// Claude clients concatenate these fragments to build the full JSON input.
				if state.started && state.argsBuf.Len() > 0 {
					writeSSEEvent(w, "content_block_delta", map[string]any{
						"type":  "content_block_delta",
						"index": contentBlockIndex,
						"delta": map[string]any{
							"type":         "input_json_delta",
							"partial_json": state.argsBuf.String(),
						},
					})
					state.argsBuf.Reset()
				}
			}
		}

		// Handle finish_reason
		if reason, ok := choiceMap["finish_reason"].(string); ok && reason != "" {
			stopReason = mapFinishReason(reason)
		}
		return true
	}

	done := false
	for !done {
		// Check for disconnection
		if isDisconnected != nil && isDisconnected() {
			if cancelFn != nil {
				cancelFn()
			}
			return
		}

		select {
		case <-ctx.Done():
			slog.Debug("streaming request context cancelled")
			if cancelFn != nil {
				cancelFn()
			}
			return
		case err := <-readErr:
			slog.Error("scanner error while reading SSE stream", "error", err)
			done = true
		case line, ok := <-lines:
			if !ok {
				done = true
				break
			}
			// Inactivity is measured between lines; any line (even a blank
			// keep-alive) counts as progress.
			if !idleTimer.Stop() {
				select {
				case <-idleTimer.C:
				default:
				}
			}
			idleTimer.Reset(StreamIdleTimeout)
			if !processLine(line) {
				done = true
			}
		case <-idleTimer.C:
			slog.Warn("upstream stream stalled; cancelling", "idle_timeout", StreamIdleTimeout)
			if cancelFn != nil {
				cancelFn()
			}
			writeSSEEvent(w, "error", map[string]any{
				"type": "error",
				"error": map[string]any{
					"type":    "api_error",
					"message": fmt.Sprintf("upstream stream stalled for %s", StreamIdleTimeout),
				},
			})
			return
		}
	}

	// Abort after an in-stream error frame: the error event is already on
	// the wire, so skip the normal message_delta/message_stop epilogue.
	if abort {
		return
	}

	// Guarantee at least one content block (empty text) so clients always
	// see a matched start/stop pair, then close the last open block.
	if contentBlockIndex == -1 {
		openBlock(map[string]any{"type": "text", "text": ""})
	}
	closeOpenBlock()

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
