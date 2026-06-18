package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"claude-code-proxy-go/internal/config"
	"claude-code-proxy-go/internal/handler"
)

func main() {
	// Parse --help flag
	if len(os.Args) > 1 && os.Args[1] == "--help" {
		fmt.Println("Claude-to-OpenAI API Proxy v1.0.0")
		fmt.Println()
		fmt.Println("A proxy server that accepts Claude API requests and forwards them to OpenAI-compatible APIs.")
		fmt.Println()
		fmt.Println("Usage:")
		fmt.Println("  claude-code-proxy [flags]")
		fmt.Println()
		fmt.Println("Flags:")
		fmt.Println("  --help    Show this help message")
		fmt.Println()
		fmt.Println("Configuration:")
		fmt.Println("  Set environment variables or source a .env file before running.")
		fmt.Println("  See .env.example for all available options.")
		os.Exit(0)
	}

	// Configuration is auto-loaded via config.init()
	if err := config.AppConfig.Validate(); err != nil {
		log.Fatalf("Configuration error: %v", err)
	}

	// Print config summary with emoji
	fmt.Println("🚀 Claude-to-OpenAI API Proxy v1.0.0")
	fmt.Println("✅ Configuration loaded successfully")
	fmt.Printf("   OpenAI Base URL: %s\n", config.AppConfig.OpenAIBaseURL)
	fmt.Printf("   Big Model:       %s\n", config.AppConfig.BigModel)
	fmt.Printf("   Middle Model:    %s\n", config.AppConfig.MiddleModel)
	fmt.Printf("   Small Model:     %s\n", config.AppConfig.SmallModel)
	fmt.Printf("   Log Level:       %s\n", config.AppConfig.LogLevel)
	fmt.Printf("   Request Timeout: %ds\n", config.AppConfig.RequestTimeout)
	fmt.Printf("   Max Retries:     %d\n", config.AppConfig.MaxRetries)
	if config.AppConfig.AnthropicAPIKey != "" {
		fmt.Println("   Client API Key:  ✅ Validation enabled")
	} else {
		fmt.Println("   Client API Key:  ⚠️  No validation (open access)")
	}
	if config.AppConfig.AzureAPIVersion != "" {
		fmt.Printf("   Azure API Ver:   %s\n", config.AppConfig.AzureAPIVersion)
	}
	fmt.Println()

	// Initialize handlers
	handler.InitHandlers()

	// Create rate limiter (100 requests per minute per IP)
	rateLimiter := handler.NewRateLimiter(100, time.Minute)
	rateLimitMiddleware := handler.RateLimitMiddleware(rateLimiter)

	// Setup routes using Go 1.22+ ServeMux pattern matching
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", rateLimitMiddleware(handler.ValidateAPIKey(handler.CreateMessage)))
	mux.HandleFunc("POST /v1/messages/count_tokens", rateLimitMiddleware(handler.ValidateAPIKey(handler.CountTokens)))
	mux.HandleFunc("GET /v1/models", rateLimitMiddleware(handler.ValidateAPIKey(handler.ListModels)))
	mux.HandleFunc("GET /health", handler.HealthCheck)
	mux.HandleFunc("GET /test-connection", rateLimitMiddleware(handler.ValidateAPIKey(handler.TestConnection)))
	mux.HandleFunc("GET /", handler.Root)

	// Add CORS middleware wrapper
	corsHandler := corsMiddleware(mux)

	// Start server
	addr := fmt.Sprintf("%s:%d", config.AppConfig.Host, config.AppConfig.Port)
	fmt.Printf("   Server: %s\n", addr)
	fmt.Println("🌐 Listening for requests...")

	server := &http.Server{
		Addr:    addr,
		Handler: corsHandler,
		// ReadHeaderTimeout guards against slow-loris attacks on header delivery.
		// ReadTimeout / WriteTimeout are intentionally 0 (unlimited) because
		// SSE streaming responses can run for the full request duration.
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Wait for interrupt signal for graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	fmt.Println("\n🛑 Shutting down server...")

	// Give outstanding requests 30 seconds to complete
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	// Stop the rate limiter's background goroutine.
	rateLimiter.Stop()

	fmt.Println("✅ Server exited gracefully")
}

func corsMiddleware(next http.Handler) http.Handler {
	// CORS_ALLOWED_ORIGINS accepts a comma-separated list of allowed origins.
	// Defaults to localhost:3000 for local development.
	raw := os.Getenv("CORS_ALLOWED_ORIGINS")
	if raw == "" {
		raw = os.Getenv("CORS_ALLOWED_ORIGIN") // backwards-compatible single-origin var
	}
	if raw == "" {
		raw = "http://localhost:3000"
	}
	allowedOrigins := make(map[string]bool)
	for _, o := range strings.Split(raw, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			allowedOrigins[o] = true
		}
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin != "" && allowedOrigins[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			// Vary: Origin tells caches that responses differ by origin.
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, x-api-key")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
