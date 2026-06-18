package model

// ContentBlockText represents a text content block in a Claude message.
type ContentBlockText struct {
	Type string `json:"type"` // always "text"
	Text string `json:"text"`
}

// ContentBlockImage represents an image content block in a Claude message.
type ContentBlockImage struct {
	Type   string         `json:"type"` // always "image"
	Source map[string]any `json:"source"`
}

// ContentBlockToolUse represents a tool_use content block in a Claude message.
type ContentBlockToolUse struct {
	Type  string         `json:"type"` // always "tool_use"
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ContentBlockToolResult represents a tool_result content block in a Claude message.
type ContentBlockToolResult struct {
	Type      string `json:"type"` // always "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"` // can be string, list, or dict
}

// SystemContent represents a system content block.
type SystemContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Message represents a single message in a Claude conversation.
type Message struct {
	Role    string `json:"role"`    // user, assistant, system
	Content any    `json:"content"` // string or []ContentBlock (use any, parse dynamically)
}

// Tool represents a tool definition in Claude format.
type Tool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

// ThinkingConfig represents the thinking/reasoning configuration.
type ThinkingConfig struct {
	Enabled bool `json:"enabled"`
}

// ToolChoice represents the tool_choice field in a Claude request.
type ToolChoice struct {
	Type string `json:"type"` // "auto", "any", or "tool"
	Name string `json:"name,omitempty"`
}

// MessagesRequest represents a Claude Messages API request.
type MessagesRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	Messages      []Message       `json:"messages"`
	System        any             `json:"system,omitempty"` // string or []SystemContent
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	TopK          *int            `json:"top_k,omitempty"`
	Metadata      map[string]any  `json:"metadata,omitempty"`
	Tools         []Tool          `json:"tools,omitempty"`
	ToolChoice    *ToolChoice     `json:"tool_choice,omitempty"`
	Thinking      *ThinkingConfig `json:"thinking,omitempty"`
}

// TokenCountRequest represents a Claude token counting request.
type TokenCountRequest struct {
	Model      string          `json:"model"`
	Messages   []Message       `json:"messages"`
	System     any             `json:"system,omitempty"`
	Tools      []Tool          `json:"tools,omitempty"`
	Thinking   *ThinkingConfig `json:"thinking,omitempty"`
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`
}

// ModelInfo represents a single model in the models list.
type ModelInfo struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	Created     int64  `json:"created"`
}

// ModelsResponse represents the response for listing available models.
type ModelsResponse struct {
	Data []ModelInfo `json:"data"`
}
