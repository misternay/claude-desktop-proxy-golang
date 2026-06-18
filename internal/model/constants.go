package model

// Roles
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleTool      = "tool"
)

// Content types
const (
	ContentText       = "text"
	ContentImage      = "image"
	ContentToolUse    = "tool_use"
	ContentToolResult = "tool_result"
)

// Tool
const (
	ToolFunction = "function"
)

// Stop reasons
const (
	StopEndTurn   = "end_turn"
	StopMaxTokens = "max_tokens"
	StopToolUse   = "tool_use"
	StopError     = "error"
)

// SSE Events
const (
	EventMessageStart      = "message_start"
	EventMessageStop       = "message_stop"
	EventMessageDelta      = "message_delta"
	EventContentBlockStart = "content_block_start"
	EventContentBlockStop  = "content_block_stop"
	EventContentBlockDelta = "content_block_delta"
	EventPing              = "ping"
)

// Delta types
const (
	DeltaText      = "text_delta"
	DeltaInputJSON = "input_json_delta"
)
