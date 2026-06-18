package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// intPtr is a tiny helper so table rows can express "explicitly zero"
// (non-nil pointer to 0) vs "absent" (nil) for *int fields.
func intPtr(v int) *int { return &v }

// ---------------------------------------------------------------------------
// buildConfig: pure function -> fully parallel, table-driven with named
// subtests. Covers defaults, per-tier fallback, per-tier override, and the
// explicit-zero-value cases that the old intOr(int) implementation broke.
// ---------------------------------------------------------------------------

func TestBuildConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raw   *rawConfig
		check func(*testing.T, *Config)
	}{
		{
			name: "all defaults when empty",
			raw:  &rawConfig{},
			check: func(t *testing.T, c *Config) {
				t.Helper()
				if c.OpenAIBaseURL != "https://api.openai.com/v1" {
					t.Errorf("OpenAIBaseURL: got %q, want default", c.OpenAIBaseURL)
				}
				if c.Host != "0.0.0.0" {
					t.Errorf("Host: got %q, want 0.0.0.0", c.Host)
				}
				if c.Port != 8082 {
					t.Errorf("Port: got %d, want 8082", c.Port)
				}
				if c.LogLevel != "INFO" {
					t.Errorf("LogLevel: got %q, want INFO", c.LogLevel)
				}
				if c.MaxTokensLimit != 4096 {
					t.Errorf("MaxTokensLimit: got %d, want 4096", c.MaxTokensLimit)
				}
				if c.MinTokensLimit != 100 {
					t.Errorf("MinTokensLimit: got %d, want 100", c.MinTokensLimit)
				}
				if c.MaxTokens != 4096 {
					t.Errorf("MaxTokens: got %d, want 4096", c.MaxTokens)
				}
				if c.MinTokens != 100 {
					t.Errorf("MinTokens: got %d, want 100", c.MinTokens)
				}
				if c.RequestTimeout != 90 {
					t.Errorf("RequestTimeout: got %d, want 90", c.RequestTimeout)
				}
				if c.MaxRetries != 2 {
					t.Errorf("MaxRetries: got %d, want 2", c.MaxRetries)
				}
				if c.BigModel != "gpt-4o" || c.MiddleModel != "gpt-4o" || c.SmallModel != "gpt-4o-mini" {
					t.Errorf("model defaults: big=%q middle=%q small=%q", c.BigModel, c.MiddleModel, c.SmallModel)
				}
			},
		},
		{
			name: "explicit zero port honored",
			raw:  &rawConfig{Port: intPtr(0)},
			check: func(t *testing.T, c *Config) {
				if c.Port != 0 {
					t.Errorf("Port: got %d, want 0 (explicit zero must not fall back)", c.Port)
				}
			},
		},
		{
			name: "explicit zero max_tokens honored",
			raw:  &rawConfig{MaxTokens: intPtr(0)},
			check: func(t *testing.T, c *Config) {
				if c.MaxTokens != 0 {
					t.Errorf("MaxTokens: got %d, want 0", c.MaxTokens)
				}
			},
		},
		{
			name: "explicit zero request_timeout honored",
			raw:  &rawConfig{RequestTimeout: intPtr(0)},
			check: func(t *testing.T, c *Config) {
				if c.RequestTimeout != 0 {
					t.Errorf("RequestTimeout: got %d, want 0", c.RequestTimeout)
				}
			},
		},
		{
			name: "explicit zero max_retries honored",
			raw:  &rawConfig{MaxRetries: intPtr(0)},
			check: func(t *testing.T, c *Config) {
				if c.MaxRetries != 0 {
					t.Errorf("MaxRetries: got %d, want 0", c.MaxRetries)
				}
			},
		},
		{
			name: "explicit zero token limits honored and inherited",
			raw:  &rawConfig{MaxTokensLimit: intPtr(0), MinTokensLimit: intPtr(0)},
			check: func(t *testing.T, c *Config) {
				if c.MaxTokensLimit != 0 {
					t.Errorf("MaxTokensLimit: got %d, want 0", c.MaxTokensLimit)
				}
				if c.MinTokensLimit != 0 {
					t.Errorf("MinTokensLimit: got %d, want 0", c.MinTokensLimit)
				}
				// MaxTokens/MinTokens inherit the (now zero) limits.
				if c.MaxTokens != 0 {
					t.Errorf("MaxTokens should inherit zero limit: got %d", c.MaxTokens)
				}
				if c.MinTokens != 0 {
					t.Errorf("MinTokens should inherit zero limit: got %d", c.MinTokens)
				}
			},
		},
		{
			name: "per-tier fallback to top-level key and url",
			raw: &rawConfig{
				OpenAIAPIKey:  "sk-top",
				OpenAIBaseURL: "https://upstream.example.com/v1",
			},
			check: func(t *testing.T, c *Config) {
				tiers := []struct{ label, name, key, url string }{
					{"big", c.BigModel, c.BigModelAPIKey, c.BigModelBaseURL},
					{"middle", c.MiddleModel, c.MiddleModelAPIKey, c.MiddleModelBaseURL},
					{"small", c.SmallModel, c.SmallModelAPIKey, c.SmallModelBaseURL},
				}
				for _, tr := range tiers {
					if tr.key != "sk-top" {
						t.Errorf("%s api key fallback: got %q, want sk-top", tr.label, tr.key)
					}
					if tr.url != "https://upstream.example.com/v1" {
						t.Errorf("%s base url fallback: got %q", tr.label, tr.url)
					}
				}
				// Empty middle.name falls back to big.name (gpt-4o default).
				if c.MiddleModel != "gpt-4o" {
					t.Errorf("middle name fallback: got %q, want gpt-4o", c.MiddleModel)
				}
			},
		},
		{
			name: "per-tier explicit override",
			raw: func() *rawConfig {
				r := &rawConfig{
					OpenAIAPIKey:  "sk-top",
					OpenAIBaseURL: "https://top.example.com/v1",
				}
				r.Models.Big.Name = "big-model"
				r.Models.Big.APIKey = "sk-big"
				r.Models.Big.BaseURL = "https://big.example.com/v1"
				r.Models.Small.Name = "small-model"
				r.Models.Small.APIKey = "sk-small"
				r.Models.Small.BaseURL = "https://small.example.com/v1"
				return r
			}(),
			check: func(t *testing.T, c *Config) {
				if c.BigModel != "big-model" || c.BigModelAPIKey != "sk-big" || c.BigModelBaseURL != "https://big.example.com/v1" {
					t.Errorf("big override: name=%q key=%q url=%q", c.BigModel, c.BigModelAPIKey, c.BigModelBaseURL)
				}
				if c.SmallModel != "small-model" || c.SmallModelAPIKey != "sk-small" || c.SmallModelBaseURL != "https://small.example.com/v1" {
					t.Errorf("small override: name=%q key=%q url=%q", c.SmallModel, c.SmallModelAPIKey, c.SmallModelBaseURL)
				}
				// Middle unset -> inherits big name.
				if c.MiddleModel != "big-model" {
					t.Errorf("middle should inherit big name: got %q, want big-model", c.MiddleModel)
				}
			},
		},
		{
			name: "custom headers passed through",
			raw: &rawConfig{
				CustomHeaders: map[string]string{"X-Custom": "v1", "X-Other": "v2"},
			},
			check: func(t *testing.T, c *Config) {
				if c.CustomHeaders["X-Custom"] != "v1" || c.CustomHeaders["X-Other"] != "v2" {
					t.Errorf("custom headers not propagated: %#v", c.CustomHeaders)
				}
				if got := c.GetCustomHeaders(); got["X-Custom"] != "v1" {
					t.Errorf("GetCustomHeaders(): %#v", got)
				}
			},
		},
		{
			name: "nil raw pointer still returns defaults",
			raw:  nil,
			check: func(t *testing.T, c *Config) {
				if c == nil {
					t.Fatal("expected non-nil Config for nil raw")
				}
				if c.Port != 8082 {
					t.Errorf("Port: got %d, want default 8082", c.Port)
				}
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := buildConfig(tc.raw)
			tc.check(t, cfg)
		})
	}
}

// ---------------------------------------------------------------------------
// parseConfigFlag: pure function of args via stdlib flag -> fully parallel.
// ---------------------------------------------------------------------------

func TestParseConfigFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		args    []string
		want    string
		wantErr bool
	}{
		{name: "absent", args: []string{}, want: ""},
		{name: "nil args", args: nil, want: ""},
		{name: "space form", args: []string{"--config", "/tmp/c.yaml"}, want: "/tmp/c.yaml"},
		{name: "equals form", args: []string{"--config=/tmp/c.yaml"}, want: "/tmp/c.yaml"},
		// The stdlib flag package rejects undefined -flags but tolerates
		// trailing positionals (they're collected as args, not errors).
		{name: "unknown flag errors", args: []string{"--foo"}, want: "", wantErr: true},
		{name: "trailing positional ok", args: []string{"extra"}, want: ""},
		// "--config" with no following value is rejected by the flag package.
		{name: "space form no value", args: []string{"--config"}, want: "", wantErr: true},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseConfigFlag(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil (path=%q)", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveConfigPath: pure function of `explicit`. The explicit-path cases
// parallelize cleanly; the cwd-based default-discovery cases cannot (they
// mutate process cwd) and are kept isolated below.
// ---------------------------------------------------------------------------

func TestResolveConfigPath_Explicit(t *testing.T) {
	t.Parallel()

	t.Run("explicit wins when file exists", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "flag.yaml")
		if err := os.WriteFile(f, []byte("openai_api_key: x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := resolveConfigPath(f)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != f {
			t.Errorf("got %q, want %q", got, f)
		}
	})

	t.Run("explicit missing returns ErrConfigFlagMissing", func(t *testing.T) {
		t.Parallel()
		_, err := resolveConfigPath(filepath.Join(t.TempDir(), "nope.yaml"))
		if !errors.Is(err, ErrConfigFlagMissing) {
			t.Fatalf("expected ErrConfigFlagMissing, got %v", err)
		}
	})
}

// NOT parallel: mutates process cwd via os.Chdir.
func TestResolveConfigPath_DefaultCwd(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfgFile, []byte("openai_api_key: x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	got, err := resolveConfigPath("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// cwd candidate is the relative form "config.yaml".
	if got != "config.yaml" {
		t.Errorf("got %q, want config.yaml", got)
	}
}

// NOT parallel: mutates process cwd via os.Chdir.
func TestResolveConfigPath_NotFound(t *testing.T) {
	dir := t.TempDir()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	_, err = resolveConfigPath("")
	if !errors.Is(err, ErrConfigNotFound) {
		t.Fatalf("expected ErrConfigNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// NewConfigFromFile: pure function of path -> fully parallel subtests.
// ---------------------------------------------------------------------------

func TestNewConfigFromFile(t *testing.T) {
	t.Parallel()

	t.Run("parses and applies fallback", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "my.yaml")
		content := `
openai_api_key: sk-from-file
openai_base_url: https://custom.example.com/v1
port: 9999
models:
  big:
    name: big-from-file
    api_key: sk-big-override
`
		if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := NewConfigFromFile(f)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cfg.OpenAIAPIKey != "sk-from-file" {
			t.Errorf("api key: got %q", cfg.OpenAIAPIKey)
		}
		if cfg.Port != 9999 {
			t.Errorf("port: got %d, want 9999", cfg.Port)
		}
		if cfg.BigModel != "big-from-file" || cfg.BigModelAPIKey != "sk-big-override" {
			t.Errorf("big override: name=%q key=%q", cfg.BigModel, cfg.BigModelAPIKey)
		}
		// small unset -> inherits top-level key.
		if cfg.SmallModelAPIKey != "sk-from-file" {
			t.Errorf("small should inherit top-level key: got %q", cfg.SmallModelAPIKey)
		}
		if cfg.OpenAIBaseURL != "https://custom.example.com/v1" {
			t.Errorf("base url: got %q", cfg.OpenAIBaseURL)
		}
	})

	t.Run("explicit port zero honored through YAML", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "zero.yaml")
		if err := os.WriteFile(f, []byte("port: 0\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := NewConfigFromFile(f)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cfg.Port != 0 {
			t.Errorf("port: got %d, want 0 (explicit zero must survive YAML round-trip)", cfg.Port)
		}
	})

	t.Run("malformed YAML returns parse error", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "bad.yaml")
		if err := os.WriteFile(f, []byte("openai_api_key: [unclosed"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := NewConfigFromFile(f)
		if err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})

	t.Run("empty file yields defaults", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		f := filepath.Join(dir, "empty.yaml")
		if err := os.WriteFile(f, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg, err := NewConfigFromFile(f)
		if err != nil {
			t.Fatalf("error: %v", err)
		}
		if cfg.Port != 8082 || cfg.BigModel != "gpt-4o" {
			t.Errorf("empty file should yield defaults: port=%d big=%q", cfg.Port, cfg.BigModel)
		}
	})
}

// ---------------------------------------------------------------------------
// Load: integration with the package-global AppConfig. NOT parallel (it
// mutates the global) but each test restores the previous value and asserts
// the failure contract (AppConfig untouched on error).
// ---------------------------------------------------------------------------

func TestLoad_AssignsAppConfig(t *testing.T) {
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })

	dir := t.TempDir()
	f := filepath.Join(dir, "via-flag.yaml")
	content := "openai_api_key: sk-load\nport: 7000\n"
	if err := os.WriteFile(f, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Load([]string{"--config", f}); err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if AppConfig == nil {
		t.Fatal("AppConfig not set")
	}
	if AppConfig.OpenAIAPIKey != "sk-load" {
		t.Errorf("AppConfig.OpenAIAPIKey: got %q, want sk-load", AppConfig.OpenAIAPIKey)
	}
	if AppConfig.Port != 7000 {
		t.Errorf("AppConfig.Port: got %d, want 7000", AppConfig.Port)
	}
}

func TestLoad_FailureLeavesAppConfigUntouched(t *testing.T) {
	prev := AppConfig
	t.Cleanup(func() { AppConfig = prev })
	AppConfig = nil

	err := Load([]string{"--config", filepath.Join(t.TempDir(), "nope.yaml")})
	if !errors.Is(err, ErrConfigFlagMissing) {
		t.Fatalf("expected ErrConfigFlagMissing, got %v", err)
	}
	if AppConfig != nil {
		t.Errorf("AppConfig must remain nil on load failure, got %+v", AppConfig)
	}
}

// ---------------------------------------------------------------------------
// Validators: pure methods on *Config -> fully parallel table-driven tests.
// ---------------------------------------------------------------------------

func TestValidate_RequiresOpenAIKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		key     string
		wantErr error
	}{
		{name: "empty key errors", key: "", wantErr: ErrMissingAPIKey},
		{name: "non-empty key ok", key: "sk-x", wantErr: nil},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{OpenAIAPIKey: tc.key}
			err := cfg.Validate()
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("Validate(%q): got %v, want %v", tc.key, err, tc.wantErr)
			}
		})
	}
}

func TestValidateClientAPIKey(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		configured string
		clientKey  string
		wantValid  bool
	}{
		{name: "no configured key is open access", configured: "", clientKey: "anything", wantValid: true},
		{name: "no configured key ignores empty client key", configured: "", clientKey: "", wantValid: true},
		{name: "matching key", configured: "secret", clientKey: "secret", wantValid: true},
		{name: "mismatched key", configured: "secret", clientKey: "wrong", wantValid: false},
		{name: "empty client key when configured", configured: "secret", clientKey: "", wantValid: false},
		{name: "same length but wrong", configured: "secret", clientKey: "secreu", wantValid: false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cfg := &Config{AnthropicAPIKey: tc.configured}
			if got := cfg.ValidateClientAPIKey(tc.clientKey); got != tc.wantValid {
				t.Errorf("ValidateClientAPIKey(%q) configured=%q: got %v, want %v",
					tc.clientKey, tc.configured, got, tc.wantValid)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Default: hermetic escape hatch for test packages that read AppConfig.
// ---------------------------------------------------------------------------

func TestDefault_YieldsHardcodedDefaults(t *testing.T) {
	t.Parallel()
	c := Default()
	if c.Port != 8082 || c.BigModel != "gpt-4o" || c.OpenAIBaseURL != "https://api.openai.com/v1" {
		t.Errorf("Default() not populated: %+v", c)
	}
}
