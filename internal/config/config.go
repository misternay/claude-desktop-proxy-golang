package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
)

// Config holds all configuration for the proxy server.
type Config struct {
	OpenAIAPIKey     string
	AnthropicAPIKey  string
	OpenAIBaseURL    string
	AzureAPIVersion  string
	Host             string
	Port             int
	LogLevel         string
	MaxTokensLimit   int
	MinTokensLimit   int
	MaxTokens        int
	MinTokens        int
	RequestTimeout   int
	MaxRetries       int
	BigModel         string
	MiddleModel      string
	SmallModel       string
	BigModelAPIKey   string
	BigModelBaseURL  string
	MiddleModelAPIKey  string
	MiddleModelBaseURL string
	SmallModelAPIKey   string
	SmallModelBaseURL  string
}

// AppConfig is the singleton configuration instance.
var AppConfig *Config

func init() {
	AppConfig = NewConfig()
}

// NewConfig creates a new Config from environment variables.
// Does not fatal on missing required fields — call Validate() after --help handling.
func NewConfig() *Config {
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")

	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicAPIKey == "" {
		log.Println("WARNING: ANTHROPIC_API_KEY is not set. Client API key validation will be skipped.")
	}

	openaiBaseURL := getEnvDefault("OPENAI_BASE_URL", "https://api.openai.com/v1")
	azureAPIVersion := os.Getenv("AZURE_API_VERSION")
	host := getEnvDefault("HOST", "0.0.0.0")
	port := getEnvInt("PORT", 8082)
	logLevel := getEnvDefault("LOG_LEVEL", "INFO")
	maxTokensLimit := getEnvInt("MAX_TOKENS_LIMIT", 4096)
	minTokensLimit := getEnvInt("MIN_TOKENS_LIMIT", 100)
	maxTokens := maxTokensLimit
	minTokens := minTokensLimit
	requestTimeout := getEnvInt("REQUEST_TIMEOUT", 90)
	maxRetries := getEnvInt("MAX_RETRIES", 2)
	bigModel := getEnvDefault("BIG_MODEL", "gpt-4o")
	middleModel := getEnvDefault("MIDDLE_MODEL", bigModel)
	smallModel := getEnvDefault("SMALL_MODEL", "gpt-4o-mini")

	bigModelAPIKey := getEnvDefault("BIG_MODEL_API_KEY", openaiAPIKey)
	bigModelBaseURL := getEnvDefault("BIG_MODEL_BASE_URL", openaiBaseURL)
	middleModelAPIKey := getEnvDefault("MIDDLE_MODEL_API_KEY", openaiAPIKey)
	middleModelBaseURL := getEnvDefault("MIDDLE_MODEL_BASE_URL", openaiBaseURL)
	smallModelAPIKey := getEnvDefault("SMALL_MODEL_API_KEY", openaiAPIKey)
	smallModelBaseURL := getEnvDefault("SMALL_MODEL_BASE_URL", openaiBaseURL)

	return &Config{
		OpenAIAPIKey:       openaiAPIKey,
		AnthropicAPIKey:    anthropicAPIKey,
		OpenAIBaseURL:      openaiBaseURL,
		AzureAPIVersion:    azureAPIVersion,
		Host:               host,
		Port:               port,
		LogLevel:           logLevel,
		MaxTokensLimit:     maxTokensLimit,
		MinTokensLimit:     minTokensLimit,
		MaxTokens:          maxTokens,
		MinTokens:          minTokens,
		RequestTimeout:     requestTimeout,
		MaxRetries:         maxRetries,
		BigModel:           bigModel,
		MiddleModel:        middleModel,
		SmallModel:         smallModel,
		BigModelAPIKey:     bigModelAPIKey,
		BigModelBaseURL:    bigModelBaseURL,
		MiddleModelAPIKey:  middleModelAPIKey,
		MiddleModelBaseURL: middleModelBaseURL,
		SmallModelAPIKey:   smallModelAPIKey,
		SmallModelBaseURL:  smallModelBaseURL,
	}
}

// Validate checks that all required configuration is present.
func (c *Config) Validate() error {
	if c.OpenAIAPIKey == "" {
		return fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}
	return nil
}

// ValidateAPIKey checks that the OpenAI API key has a valid prefix.
func (c *Config) ValidateAPIKey() error {
	if !strings.HasPrefix(c.OpenAIAPIKey, "sk-") {
		return fmt.Errorf("OpenAI API key must start with 'sk-', got: %s", c.OpenAIAPIKey[:min(5, len(c.OpenAIAPIKey))]+"...")
	}
	return nil
}

// ValidateClientAPIKey checks the provided client key against the configured Anthropic API key.
func (c *Config) ValidateClientAPIKey(clientKey string) bool {
	if c.AnthropicAPIKey == "" {
		// No Anthropic key configured, skip validation
		return true
	}
	return clientKey == c.AnthropicAPIKey
}

// GetCustomHeaders reads CUSTOM_HEADER_* environment variables and returns them as a map.
func (c *Config) GetCustomHeaders() map[string]string {
	headers := make(map[string]string)
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "CUSTOM_HEADER_") {
			parts := strings.SplitN(env, "=", 2)
			if len(parts) == 2 {
				// Convert CUSTOM_HEADER_X_FOO to X-Foo
				key := strings.TrimPrefix(parts[0], "CUSTOM_HEADER_")
				key = strings.ReplaceAll(key, "_", "-")
				headers[key] = parts[1]
			}
		}
	}
	return headers
}

func getEnvDefault(key, defaultValue string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultValue
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		log.Printf("WARNING: Invalid integer for %s=%q, using default %d", key, val, defaultValue)
		return defaultValue
	}
	return n
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
