package converter

import (
	"encoding/json"
	"testing"

	"claude-code-proxy-go/internal/config"
	"claude-code-proxy-go/internal/model"
	"claude-code-proxy-go/internal/modelmanager"
)

func init() {
	// Ensure config is loaded for tests
	if config.AppConfig == nil {
		// Set a test API key so NewConfig doesn't panic
		// (config.init() already ran, but we may need to override)
	}
}

func newTestModelManager() *modelmanager.ModelManager {
	return modelmanager.NewModelManager(config.AppConfig)
}

func TestConvertSimpleTextRequest(t *testing.T) {
	mm := newTestModelManager()

	req := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 100,
		Messages: []model.Message{
			{Role: "user", Content: "Hello, how are you?"},
		},
		Temperature: floatPtr(0.7),
	}

	result := ConvertClaudeToOpenAI(req, mm)

	// Check model was mapped
	if result["model"] == "" {
		t.Error("expected model to be mapped")
	}

	// Check messages
	messages, ok := result["messages"].([]map[string]any)
	if !ok {
		t.Fatal("expected messages to be []map[string]any")
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0]["role"] != "user" {
		t.Errorf("expected role 'user', got %v", messages[0]["role"])
	}
	if messages[0]["content"] != "Hello, how are you?" {
		t.Errorf("expected content 'Hello, how are you?', got %v", messages[0]["content"])
	}
}

func TestConvertWithSystemMessage(t *testing.T) {
	mm := newTestModelManager()

	req := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 100,
		System:    "You are a helpful assistant.",
		Messages: []model.Message{
			{Role: "user", Content: "Hi"},
		},
	}

	result := ConvertClaudeToOpenAI(req, mm)

	messages := result["messages"].([]map[string]any)
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages (system + user), got %d", len(messages))
	}
	if messages[0]["role"] != "system" {
		t.Errorf("expected first message role 'system', got %v", messages[0]["role"])
	}
	if messages[0]["content"] != "You are a helpful assistant." {
		t.Errorf("expected system content, got %v", messages[0]["content"])
	}
}

func TestConvertWithTools(t *testing.T) {
	mm := newTestModelManager()

	req := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 200,
		Messages: []model.Message{
			{Role: "user", Content: "What's the weather?"},
		},
		Tools: []model.Tool{
			{
				Name:        "get_weather",
				Description: "Get weather for a location",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"location": map[string]any{"type": "string"},
					},
				},
			},
		},
		ToolChoice: &model.ToolChoice{Type: "auto"},
	}

	result := ConvertClaudeToOpenAI(req, mm)

	tools, ok := result["tools"].([]map[string]any)
	if !ok {
		t.Fatal("expected tools to be present")
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(tools))
	}
	if tools[0]["type"] != "function" {
		t.Errorf("expected tool type 'function', got %v", tools[0]["type"])
	}

	fn, ok := tools[0]["function"].(map[string]any)
	if !ok {
		t.Fatal("expected function field in tool")
	}
	if fn["name"] != "get_weather" {
		t.Errorf("expected function name 'get_weather', got %v", fn["name"])
	}

	if result["tool_choice"] != "auto" {
		t.Errorf("expected tool_choice 'auto', got %v", result["tool_choice"])
	}
}

func TestConvertMaxTokensClamping(t *testing.T) {
	mm := newTestModelManager()

	// Test clamping above max
	req := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 99999,
		Messages:  []model.Message{{Role: "user", Content: "Hi"}},
	}
	result := ConvertClaudeToOpenAI(req, mm)
	maxTokens, ok := result["max_tokens"].(int)
	if !ok {
		t.Fatal("expected max_tokens to be int")
	}
	if maxTokens > config.AppConfig.MaxTokensLimit {
		t.Errorf("expected max_tokens clamped to %d, got %d", config.AppConfig.MaxTokensLimit, maxTokens)
	}

	// Test clamping below min
	req2 := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 1,
		Messages:  []model.Message{{Role: "user", Content: "Hi"}},
	}
	result2 := ConvertClaudeToOpenAI(req2, mm)
	maxTokens2 := result2["max_tokens"].(int)
	if maxTokens2 < config.AppConfig.MinTokensLimit {
		t.Errorf("expected max_tokens clamped to at least %d, got %d", config.AppConfig.MinTokensLimit, maxTokens2)
	}
}

func TestConvertOpenAIResponse(t *testing.T) {
	openaiResp := map[string]any{
		"id": "chatcmpl-123",
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": "Hello! How can I help you?",
				},
				"finish_reason": "stop",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(10),
			"completion_tokens": float64(8),
		},
	}

	req := &model.MessagesRequest{
		Model: "claude-3-5-sonnet-20241022",
	}

	result := ConvertOpenAIToClaudeResponse(openaiResp, req)

	if result["type"] != "message" {
		t.Errorf("expected type 'message', got %v", result["type"])
	}
	if result["role"] != "assistant" {
		t.Errorf("expected role 'assistant', got %v", result["role"])
	}
	if result["stop_reason"] != "end_turn" {
		t.Errorf("expected stop_reason 'end_turn', got %v", result["stop_reason"])
	}

	content, ok := result["content"].([]map[string]any)
	if !ok {
		t.Fatal("expected content to be []map[string]any")
	}
	if len(content) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(content))
	}
	if content[0]["type"] != "text" {
		t.Errorf("expected content block type 'text', got %v", content[0]["type"])
	}
	if content[0]["text"] != "Hello! How can I help you?" {
		t.Errorf("unexpected text: %v", content[0]["text"])
	}

	usage, ok := result["usage"].(map[string]any)
	if !ok {
		t.Fatal("expected usage to be map")
	}
	if usage["input_tokens"] != 10 {
		t.Errorf("expected input_tokens 10, got %v", usage["input_tokens"])
	}
}

func TestConvertOpenAIResponseWithToolCalls(t *testing.T) {
	openaiResp := map[string]any{
		"id": "chatcmpl-456",
		"choices": []any{
			map[string]any{
				"message": map[string]any{
					"role":    "assistant",
					"content": nil,
					"tool_calls": []any{
						map[string]any{
							"id":   "call_abc",
							"type": "function",
							"function": map[string]any{
								"name":      "get_weather",
								"arguments": `{"location":"New York","unit":"celsius"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]any{
			"prompt_tokens":     float64(20),
			"completion_tokens": float64(15),
		},
	}

	req := &model.MessagesRequest{
		Model: "claude-3-5-sonnet-20241022",
	}

	result := ConvertOpenAIToClaudeResponse(openaiResp, req)

	if result["stop_reason"] != "tool_use" {
		t.Errorf("expected stop_reason 'tool_use', got %v", result["stop_reason"])
	}

	content := result["content"].([]map[string]any)
	// Should have tool_use block
	found := false
	for _, block := range content {
		if block["type"] == "tool_use" {
			found = true
			if block["id"] != "call_abc" {
				t.Errorf("expected tool_use id 'call_abc', got %v", block["id"])
			}
			if block["name"] != "get_weather" {
				t.Errorf("expected tool_use name 'get_weather', got %v", block["name"])
			}
			input, ok := block["input"].(map[string]any)
			if !ok {
				t.Fatal("expected input to be map")
			}
			if input["location"] != "New York" {
				t.Errorf("expected location 'New York', got %v", input["location"])
			}
		}
	}
	if !found {
		t.Error("expected tool_use content block")
	}
}

func TestParseToolResultContent(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		expected string
	}{
		{"string", "hello", "hello"},
		{"nil", nil, ""},
		{"list with text", []any{map[string]any{"type": "text", "text": "result data"}}, "result data"},
		{"dict with text", map[string]any{"type": "text", "text": "dict text"}, `{"text":"dict text","type":"text"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseToolResultContent(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

func TestConvertWithStopSequences(t *testing.T) {
	mm := newTestModelManager()

	req := &model.MessagesRequest{
		Model:         "claude-3-5-sonnet-20241022",
		MaxTokens:     100,
		Messages:      []model.Message{{Role: "user", Content: "Hi"}},
		StopSequences: []string{"END", "STOP"},
	}

	result := ConvertClaudeToOpenAI(req, mm)

	stop, ok := result["stop"].([]string)
	if !ok {
		t.Fatal("expected stop to be []string")
	}
	if len(stop) != 2 || stop[0] != "END" || stop[1] != "STOP" {
		t.Errorf("unexpected stop sequences: %v", stop)
	}
}

func TestConvertOpenAIResponseFinishReasonMapping(t *testing.T) {
	tests := []struct {
		finishReason string
		expectedStop string
	}{
		{"stop", "end_turn"},
		{"length", "max_tokens"},
		{"tool_calls", "tool_use"},
		{"function_call", "end_turn"}, // Go impl doesn't map function_call separately
		{"unknown", "end_turn"},
	}

	for _, tt := range tests {
		t.Run(tt.finishReason, func(t *testing.T) {
			openaiResp := map[string]any{
				"id": "test",
				"choices": []any{
					map[string]any{
						"message":       map[string]any{"role": "assistant", "content": "test"},
						"finish_reason": tt.finishReason,
					},
				},
				"usage": map[string]any{"prompt_tokens": 1, "completion_tokens": 1},
			}

			result := ConvertOpenAIToClaudeResponse(openaiResp, &model.MessagesRequest{Model: "test"})
			if result["stop_reason"] != tt.expectedStop {
				t.Errorf("for finish_reason=%q: expected stop_reason=%q, got %v",
					tt.finishReason, tt.expectedStop, result["stop_reason"])
			}
		})
	}
}

func TestModelManagerMapping(t *testing.T) {
	mm := newTestModelManager()

	tests := []struct {
		input    string
		expected string
	}{
		{"claude-3-5-haiku-20241022", config.AppConfig.SmallModel},
		{"claude-3-5-sonnet-20241022", config.AppConfig.MiddleModel},
		{"claude-3-opus-20240229", config.AppConfig.BigModel},
		{"gpt-4o", "gpt-4o"},
		{"deepseek-chat", "deepseek-chat"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := mm.MapClaudeModelToOpenAI(tt.input)
			if result != tt.expected {
				t.Errorf("for %q: expected %q, got %q", tt.input, tt.expected, result)
			}
		})
	}
}

// Ensure JSON marshaling of converted requests works
func TestConvertedRequestJSON(t *testing.T) {
	mm := newTestModelManager()

	req := &model.MessagesRequest{
		Model:     "claude-3-5-sonnet-20241022",
		MaxTokens: 100,
		Messages: []model.Message{
			{Role: "user", Content: "Hello"},
		},
	}

	result := ConvertClaudeToOpenAI(req, mm)

	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if parsed["model"] == nil || parsed["model"] == "" {
		t.Error("expected model in JSON output")
	}
}

func floatPtr(f float64) *float64 {
	return &f
}
