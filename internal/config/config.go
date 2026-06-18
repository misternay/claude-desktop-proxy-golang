// Package config loads and validates the proxy's YAML configuration.
package config

import (
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Sentinel errors for expected load-time conditions. Callers match these with
// errors.Is to separate "no config present" from "config present but broken",
// without parsing the error string.
var (
	// ErrConfigFlagMissing is returned when the --config flag points at a path
	// that does not exist on disk.
	ErrConfigFlagMissing = errors.New("config file from --config not found")
	// ErrConfigNotFound is returned when no config file exists at the --config
	// path or any default search location.
	ErrConfigNotFound = errors.New("no config file found")
	// ErrMissingAPIKey is returned by Validate when openai_api_key is unset.
	ErrMissingAPIKey = errors.New("openai_api_key is required")
)

// Config holds the fully-resolved configuration consumed by the rest of the
// binary. It is a runtime value: all YAML decoding happens against rawConfig,
// so this struct intentionally carries no struct tags.
type Config struct {
	// Secrets.
	OpenAIAPIKey    string
	AnthropicAPIKey string

	// Upstream.
	OpenAIBaseURL   string
	AzureAPIVersion string

	// Server.
	Host     string
	Port     int
	LogLevel string

	// Token limits.
	MaxTokensLimit int
	MinTokensLimit int
	MaxTokens      int
	MinTokens      int

	// Connection.
	RequestTimeout int
	MaxRetries     int

	// Three-tier model routing, resolved after per-tier fallback.
	BigModel           string
	MiddleModel        string
	SmallModel         string
	BigModelAPIKey     string
	BigModelBaseURL    string
	MiddleModelAPIKey  string
	MiddleModelBaseURL string
	SmallModelAPIKey   string
	SmallModelBaseURL  string

	// CustomHeaders are sent on every upstream request.
	CustomHeaders map[string]string
}

// modelConfig is one tier (big/middle/small) of the routing block.
type modelConfig struct {
	Name    string `yaml:"name"`
	APIKey  string `yaml:"api_key"`
	BaseURL string `yaml:"base_url"`
}

// modelsConfig is the three-tier routing block, extracted from rawConfig so
// it can be referenced independently in signatures and tests.
type modelsConfig struct {
	Big    modelConfig `yaml:"big"`
	Middle modelConfig `yaml:"middle"`
	Small  modelConfig `yaml:"small"`
}

// rawConfig mirrors config.yaml before fallback defaults are applied.
//
// Numeric fields use *int so that nil means "unset" and an explicit zero
// (e.g. `port: 0` or `max_retries: 0`) is preserved verbatim instead of being
// silently replaced by the default. String fields stay plain strings because
// empty-string has no analogous "valid explicit value" use case here.
type rawConfig struct {
	OpenAIAPIKey    string `yaml:"openai_api_key"`
	AnthropicAPIKey string `yaml:"anthropic_api_key"`
	OpenAIBaseURL   string `yaml:"openai_base_url"`
	AzureAPIVersion string `yaml:"azure_api_version"`

	Host     string `yaml:"host"`
	Port     *int   `yaml:"port"`
	LogLevel string `yaml:"log_level"`

	MaxTokensLimit *int `yaml:"max_tokens_limit"`
	MinTokensLimit *int `yaml:"min_tokens_limit"`
	MaxTokens      *int `yaml:"max_tokens"`
	MinTokens      *int `yaml:"min_tokens"`

	RequestTimeout *int `yaml:"request_timeout"`
	MaxRetries     *int `yaml:"max_retries"`

	Models        modelsConfig      `yaml:"models"`
	CustomHeaders map[string]string `yaml:"custom_headers"`
}

// AppConfig is the write-once singleton configuration instance, populated by
// Load for the production binary. Only Load writes it; everything else
// (handler, middleware, converter) reads it after Load has returned nil.
//
// Test packages that exercise code reading AppConfig must bootstrap it
// themselves, e.g. via `config.AppConfig = config.Default()` in TestMain.
var AppConfig *Config

// Load parses CLI args (--config), resolves and parses the config file,
// applies fallback defaults, and stores the result in AppConfig. main()
// decides whether to fatal on the returned error so --help can short-circuit
// before any fatal load. The config path is threaded explicitly; no
// package-level path state is read or mutated, so Load is reentrant.
func Load(args []string) error {
	explicit, err := parseConfigFlag(args)
	if err != nil {
		return err
	}
	cfg, err := loadConfig(explicit)
	if err != nil {
		return err
	}
	AppConfig = cfg
	return nil
}

// NewConfig runs the default discovery (no --config) and returns the result.
// Kept as a convenience entry point for callers/tests that want discovery
// without parsing CLI args.
func NewConfig() (*Config, error) {
	return loadConfig("")
}

// NewConfigFromFile loads from an explicit path. An empty path triggers the
// default discovery. A non-empty path that does not exist is an error.
func NewConfigFromFile(path string) (*Config, error) {
	return loadConfig(path)
}

// loadConfig resolves the file location, reads it, and applies defaults.
func loadConfig(explicit string) (*Config, error) {
	resolved, err := resolveConfigPath(explicit)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", resolved, err)
	}
	var raw rawConfig
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", resolved, err)
	}
	return buildConfig(&raw), nil
}

// parseConfigFlag extracts --config <path> / --config=path from args using the
// stdlib flag package. Returns the empty string when --config is absent.
// Output is discarded: main() owns the help/usage UX.
func parseConfigFlag(args []string) (string, error) {
	fs := flag.NewFlagSet("claude-code-proxy", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("config", "", "path to config.yaml (overrides default search)")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	return *path, nil
}

// resolveConfigPath picks the config file location in priority order:
//  1. explicit (from --config) — must exist or ErrConfigFlagMissing is returned
//  2. ./config.yaml
//  3. ~/.claude-code-proxy/config.yaml
//  4. /etc/claude-code-proxy/config.yaml
//
// If none of the default locations exist, resolveConfigPath returns an
// ErrConfigNotFound error whose message names the searched candidates.
func resolveConfigPath(explicit string) (string, error) {
	if explicit != "" {
		if !fileExists(explicit) {
			return "", fmt.Errorf("%w: %s", ErrConfigFlagMissing, explicit)
		}
		return explicit, nil
	}

	candidates := []string{"config.yaml"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates, filepath.Join(home, ".claude-code-proxy", "config.yaml"))
	}
	candidates = append(candidates, "/etc/claude-code-proxy/config.yaml")

	for _, c := range candidates {
		if fileExists(c) {
			return c, nil
		}
	}
	return "", fmt.Errorf(
		"%w. searched: %s. create one (see config.example.yaml) or pass --config <path>",
		ErrConfigNotFound,
		strings.Join(candidates, ", "),
	)
}

// fileExists reports whether p exists and is not a directory.
func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}

// buildConfig applies the fallback chain (per-tier -> top-level -> hardcoded
// default) and returns the public Config. This is the only place defaults
// live. Numeric fields use intOr so an explicit zero in YAML is honored.
func buildConfig(raw *rawConfig) *Config {
	if raw == nil {
		raw = &rawConfig{}
	}

	openaiBaseURL := strOr(raw.OpenAIBaseURL, "https://api.openai.com/v1")
	maxTokensLimit := intOr(raw.MaxTokensLimit, 4096)
	minTokensLimit := intOr(raw.MinTokensLimit, 100)

	big := raw.Models.Big
	middle := raw.Models.Middle
	small := raw.Models.Small

	bigName := strOr(big.Name, "gpt-4o")
	middleName := strOr(middle.Name, bigName)
	smallName := strOr(small.Name, "gpt-4o-mini")

	return &Config{
		OpenAIAPIKey:    raw.OpenAIAPIKey,
		AnthropicAPIKey: raw.AnthropicAPIKey,
		OpenAIBaseURL:   openaiBaseURL,
		AzureAPIVersion: raw.AzureAPIVersion,

		Host:           strOr(raw.Host, "0.0.0.0"),
		Port:           intOr(raw.Port, 8082),
		LogLevel:       strOr(raw.LogLevel, "INFO"),
		MaxTokensLimit: maxTokensLimit,
		MinTokensLimit: minTokensLimit,
		MaxTokens:      intOr(raw.MaxTokens, maxTokensLimit),
		MinTokens:      intOr(raw.MinTokens, minTokensLimit),
		RequestTimeout: intOr(raw.RequestTimeout, 90),
		MaxRetries:     intOr(raw.MaxRetries, 2),

		BigModel:           bigName,
		MiddleModel:        middleName,
		SmallModel:         smallName,
		BigModelAPIKey:     strOr(big.APIKey, raw.OpenAIAPIKey),
		BigModelBaseURL:    strOr(big.BaseURL, openaiBaseURL),
		MiddleModelAPIKey:  strOr(middle.APIKey, raw.OpenAIAPIKey),
		MiddleModelBaseURL: strOr(middle.BaseURL, openaiBaseURL),
		SmallModelAPIKey:   strOr(small.APIKey, raw.OpenAIAPIKey),
		SmallModelBaseURL:  strOr(small.BaseURL, openaiBaseURL),

		CustomHeaders: raw.CustomHeaders,
	}
}

// Default returns a Config populated entirely from the hardcoded defaults.
// Test packages that exercise code reading AppConfig should bootstrap it via
// `config.AppConfig = config.Default()` (typically in a TestMain).
func Default() *Config {
	return buildConfig(&rawConfig{})
}

// GetCustomHeaders returns the configured custom upstream headers.
// Kept for handler.go compatibility; the map is populated at load time.
func (c *Config) GetCustomHeaders() map[string]string {
	return c.CustomHeaders
}

// Validate checks that all required configuration is present. Currently only
// the OpenAI API key is mandatory. Returns ErrMissingAPIKey when unset so
// callers can distinguish this from load errors via errors.Is.
func (c *Config) Validate() error {
	if c.OpenAIAPIKey == "" {
		return ErrMissingAPIKey
	}
	return nil
}

// ValidateClientAPIKey checks the provided client key against the configured
// Anthropic API key using constant-time comparison to prevent timing attacks.
// When no Anthropic key is configured, client validation is disabled
// (returns true).
func (c *Config) ValidateClientAPIKey(clientKey string) bool {
	if c.AnthropicAPIKey == "" {
		return true
	}
	return subtle.ConstantTimeCompare([]byte(clientKey), []byte(c.AnthropicAPIKey)) == 1
}

// strOr returns v if non-empty, else fallback.
func strOr(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// intOr returns *v when set, else fallback. The pointer form preserves an
// explicit zero in the source YAML instead of falling back to the default.
func intOr(v *int, fallback int) int {
	if v != nil {
		return *v
	}
	return fallback
}
