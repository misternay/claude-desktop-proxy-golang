package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"claude-code-proxy-go/internal/config"
)

// ValidateAPIKey is middleware that validates the client's API key against the configured Anthropic API key.
func ValidateAPIKey(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// If ANTHROPIC_API_KEY not set in config, skip validation
		if config.AppConfig.AnthropicAPIKey == "" {
			next(w, r)
			return
		}

		// Extract from x-api-key header or Authorization: Bearer xxx
		clientKey := r.Header.Get("x-api-key")
		if clientKey == "" {
			auth := r.Header.Get("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				clientKey = strings.TrimPrefix(auth, "Bearer ")
			}
		}

		// Validate
		if !config.AppConfig.ValidateClientAPIKey(clientKey) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]any{
				"detail": "Invalid API key. Please provide a valid Anthropic API key.",
			})
			return
		}

		next(w, r)
	}
}
