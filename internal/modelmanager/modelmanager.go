package modelmanager

import (
	"claude-code-proxy-go/internal/config"
	"strings"
)

// ModelManager handles mapping Claude model names to OpenAI-compatible models.
type ModelManager struct {
	cfg *config.Config
}

// NewModelManager creates a new ModelManager with the given config.
func NewModelManager(cfg *config.Config) *ModelManager {
	return &ModelManager{cfg: cfg}
}

// GetModelConfig maps a Claude model name to the corresponding OpenAI model name,
// API key, and base URL. Returns (openaiModel, apiKey, baseURL).
func (m *ModelManager) GetModelConfig(claudeModel string) (string, string, string) {
	// If already an OpenAI model, return as-is
	if strings.HasPrefix(claudeModel, "gpt-") || strings.HasPrefix(claudeModel, "o1-") {
		return claudeModel, m.cfg.OpenAIAPIKey, m.cfg.OpenAIBaseURL
	}

	// Other known OpenAI-compatible providers
	if strings.HasPrefix(claudeModel, "ep-") || strings.HasPrefix(claudeModel, "doubao-") || strings.HasPrefix(claudeModel, "deepseek-") {
		return claudeModel, m.cfg.OpenAIAPIKey, m.cfg.OpenAIBaseURL
	}

	// Map Claude models by naming patterns
	modelLower := strings.ToLower(claudeModel)

	if strings.Contains(modelLower, "haiku") {
		return m.cfg.SmallModel, m.cfg.SmallModelAPIKey, m.cfg.SmallModelBaseURL
	}

	if strings.Contains(modelLower, "sonnet") {
		return m.cfg.MiddleModel, m.cfg.MiddleModelAPIKey, m.cfg.MiddleModelBaseURL
	}

	if strings.Contains(modelLower, "opus") {
		return m.cfg.BigModel, m.cfg.BigModelAPIKey, m.cfg.BigModelBaseURL
	}

	// Default to big model
	return m.cfg.BigModel, m.cfg.BigModelAPIKey, m.cfg.BigModelBaseURL
}

// MapClaudeModelToOpenAI maps a Claude model name to the corresponding OpenAI model name.
// Returns the mapped model name as a string.
func (m *ModelManager) MapClaudeModelToOpenAI(claudeModel string) string {
	modelName, _, _ := m.GetModelConfig(claudeModel)
	return modelName
}
