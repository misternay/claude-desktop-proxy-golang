package converter

import (
	"claude-code-proxy-go/internal/config"
	"claude-code-proxy-go/internal/model"
	"claude-code-proxy-go/internal/modelmanager"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// ConvertClaudeToOpenAI converts a Claude MessagesRequest to an OpenAI chat completion request.
func ConvertClaudeToOpenAI(req *model.MessagesRequest, mm *modelmanager.ModelManager) map[string]any {
	// Map model name
	openaiModel := req.Model
	if mm != nil {
		mapped := mm.MapClaudeModelToOpenAI(req.Model)
		if mapped != "" {
			openaiModel = mapped
		}
	}

	// Build messages array
	var messages []map[string]any

	// System message
	systemContent := extractSystemContent(req.System)
	if systemContent != "" {
		messages = append(messages, map[string]any{
			"role":    "system",
			"content": systemContent,
		})
	}

	// Process conversation messages
	for i, msg := range req.Messages {
		switch msg.Role {
		case model.RoleUser:
			userMsg := convertClaudeUserMessage(&msg)
			messages = append(messages, userMsg)
		case model.RoleSystem:
			sysMsg := convertClaudeSystemMessage(&msg)
			messages = append(messages, sysMsg)
		case model.RoleAssistant:
			assistantMsg := convertClaudeAssistantMessage(&msg)
			messages = append(messages, assistantMsg)

			// Check if next message has tool_result blocks
			if i+1 < len(req.Messages) {
				nextMsg := req.Messages[i+1]
				toolResults := convertClaudeToolResults(&nextMsg)
				if len(toolResults) > 0 {
					messages = append(messages, toolResults...)
				}
			}
		}
	}

	// Build the request
	cfg := config.AppConfig

	// Clamp max_tokens between config min and max
	maxTokens := req.MaxTokens
	if maxTokens < cfg.MinTokens {
		maxTokens = cfg.MinTokens
	}
	if maxTokens > cfg.MaxTokens {
		maxTokens = cfg.MaxTokens
	}

	result := map[string]any{
		"model":      openaiModel,
		"messages":   messages,
		"max_tokens": maxTokens,
		"stream":     req.Stream,
	}

	// Temperature
	if req.Temperature != nil {
		result["temperature"] = *req.Temperature
	}

	// Stop sequences
	if len(req.StopSequences) > 0 {
		result["stop"] = req.StopSequences
	}

	// Top P
	if req.TopP != nil {
		result["top_p"] = *req.TopP
	}

	// Convert tools
	if len(req.Tools) > 0 {
		openaiTools := convertTools(req.Tools)
		result["tools"] = openaiTools
	}

	// Convert tool choice
	if req.ToolChoice != nil {
		result["tool_choice"] = convertToolChoice(req.ToolChoice)
	}

	return result
}

// extractSystemContent extracts text from the system field which can be a string or []SystemContent.
func extractSystemContent(system any) string {
	if system == nil {
		return ""
	}
	switch v := system.(type) {
	case string:
		return v
	case []any:
		var parts []string
		for _, item := range v {
			if m, ok := item.(map[string]any); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "\n")
		}
	}
	return ""
}

// convertClaudeUserMessage converts a Claude user message to OpenAI format.
func convertClaudeUserMessage(msg *model.Message) map[string]any {
	content := msg.Content

	// Simple string content
	if str, ok := content.(string); ok {
		return map[string]any{
			"role":    "user",
			"content": str,
		}
	}

	// Multimodal content (array of content blocks)
	if blocks, ok := content.([]any); ok {
		hasImages := false
		for _, block := range blocks {
			if m, ok := block.(map[string]any); ok {
				if m["type"] == model.ContentImage {
					hasImages = true
					break
				}
			}
		}

		if !hasImages {
			// Only text blocks, concatenate
			var texts []string
			for _, block := range blocks {
				if m, ok := block.(map[string]any); ok {
					if m["type"] == model.ContentText {
						if text, ok := m["text"].(string); ok {
							texts = append(texts, text)
						}
					}
				}
			}
			return map[string]any{
				"role":    "user",
				"content": strings.Join(texts, "\n"),
			}
		}

		// Has images - use multimodal format
		var contentParts []map[string]any
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				continue
			}
			switch m["type"] {
			case model.ContentText:
				if text, ok := m["text"].(string); ok {
					contentParts = append(contentParts, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			case model.ContentImage:
				source, _ := m["source"].(map[string]any)
				if source != nil {
					sourceType, _ := source["type"].(string)
					if sourceType == "base64" {
						mediaType, _ := source["media_type"].(string)
						data, _ := source["data"].(string)
						contentParts = append(contentParts, map[string]any{
							"type": "image_url",
							"image_url": map[string]any{
								"url": fmt.Sprintf("data:%s;base64,%s", mediaType, data),
							},
						})
					}
				}
			}
		}
		return map[string]any{
			"role":    "user",
			"content": contentParts,
		}
	}

	// Fallback
	return map[string]any{
		"role":    "user",
		"content": fmt.Sprintf("%v", content),
	}
}

// convertClaudeSystemMessage converts a Claude system message to OpenAI format.
func convertClaudeSystemMessage(msg *model.Message) map[string]any {
	text := extractTextFromContent(msg.Content)
	return map[string]any{
		"role":    "system",
		"content": text,
	}
}

// convertClaudeAssistantMessage converts a Claude assistant message to OpenAI format.
func convertClaudeAssistantMessage(msg *model.Message) map[string]any {
	result := map[string]any{
		"role": "assistant",
	}

	text := extractTextFromContent(msg.Content)
	if text != "" {
		result["content"] = text
	} else {
		result["content"] = ""
	}

	// Extract tool calls from content blocks
	if blocks, ok := msg.Content.([]any); ok {
		var toolCalls []map[string]any
		for _, block := range blocks {
			m, ok := block.(map[string]any)
			if !ok {
				continue
			}
			if m["type"] == model.ContentToolUse {
				input := m["input"]
				var argsJSON string
				if inputBytes, err := json.Marshal(input); err == nil {
					argsJSON = string(inputBytes)
				} else {
					argsJSON = "{}"
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   m["id"],
					"type": "function",
					"function": map[string]any{
						"name":      m["name"],
						"arguments": argsJSON,
					},
				})
			}
		}
		if len(toolCalls) > 0 {
			result["tool_calls"] = toolCalls
		}
	}

	return result
}

// convertClaudeToolResults processes the next message for tool_result blocks and returns tool role messages.
func convertClaudeToolResults(msg *model.Message) []map[string]any {
	if msg.Role != model.RoleUser {
		return nil
	}

	blocks, ok := msg.Content.([]any)
	if !ok {
		return nil
	}

	var results []map[string]any
	for _, block := range blocks {
		m, ok := block.(map[string]any)
		if !ok {
			continue
		}
		if m["type"] == model.ContentToolResult {
			toolUseID, _ := m["tool_use_id"].(string)
			content := parseToolResultContent(m["content"])
			results = append(results, map[string]any{
				"role":         "tool",
				"tool_call_id": toolUseID,
				"content":      content,
			})
		}
	}

	return results
}

// parseToolResultContent extracts text from tool result content.
func parseToolResultContent(content any) string {
	if content == nil {
		return ""
	}
	if str, ok := content.(string); ok {
		return str
	}
	if blocks, ok := content.([]any); ok {
		var texts []string
		for _, block := range blocks {
			if m, ok := block.(map[string]any); ok {
				if m["type"] == model.ContentText {
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	// Fallback: marshal to JSON string
	if b, err := json.Marshal(content); err == nil {
		return string(b)
	}
	return fmt.Sprintf("%v", content)
}

// extractTextFromContent extracts text from content which can be string or content blocks.
func extractTextFromContent(content any) string {
	if content == nil {
		return ""
	}
	if str, ok := content.(string); ok {
		return str
	}
	if blocks, ok := content.([]any); ok {
		var texts []string
		for _, block := range blocks {
			if m, ok := block.(map[string]any); ok {
				if m["type"] == model.ContentText {
					if text, ok := m["text"].(string); ok {
						texts = append(texts, text)
					}
				}
			}
		}
		if len(texts) > 0 {
			return strings.Join(texts, "\n")
		}
	}
	return ""
}

// convertTools converts Claude tools to OpenAI function tools.
func convertTools(tools []model.Tool) []map[string]any {
	var result []map[string]any
	for _, tool := range tools {
		openaiTool := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  tool.InputSchema,
			},
		}
		result = append(result, openaiTool)
	}
	return result
}

// convertToolChoice converts Claude tool_choice to OpenAI format.
func convertToolChoice(toolChoice *model.ToolChoice) any {
	if toolChoice == nil {
		return nil
	}

	switch toolChoice.Type {
	case "auto":
		return "auto"
	case "any":
		return "auto"
	case "tool":
		return map[string]any{
			"type": "function",
			"function": map[string]any{
				"name": toolChoice.Name,
			},
		}
	default:
		slog.Warn("unknown tool_choice type, defaulting to auto", "type", toolChoice.Type)
		return "auto"
	}
}
