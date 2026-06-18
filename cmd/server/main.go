package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

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
		fmt.Println("  Set environment variables or use a .env file.")
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

	// Setup routes using Go 1.22+ ServeMux pattern matching
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/messages", handler.ValidateAPIKey(handler.CreateMessage))
	mux.HandleFunc("POST /v1/messages/count_tokens", handler.ValidateAPIKey(handler.CountTokens))
	mux.HandleFunc("GET /v1/models", handler.ValidateAPIKey(handler.ListModels))
	mux.HandleFunc("GET /health", handler.HealthCheck)
	mux.HandleFunc("GET /test-connection", handler.TestConnection)
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
	}
	log.Fatal(server.ListenAndServe())
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}
