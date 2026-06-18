# Claude Code Proxy (Go)

A high-performance proxy server that accepts **Claude API** format requests and forwards them to **OpenAI-compatible** API providers. Written in Go with **zero external dependencies** — only the standard library.

## Why Go?

- **8.9 MB** static binary — no Python runtime needed
- **~2,500 lines** of clean, idiomatic Go
- **Zero dependencies** — builds with just `go build`
- **Fast startup** — ready in milliseconds
- **Single binary deployment** — easy to distribute

## Features

- Converts Claude Messages API → OpenAI Chat Completions
- Streaming (SSE) and non-streaming responses
- Tool/function calling with streaming deltas
- Multimodal input (text + base64 images)
- Model mapping: haiku → small, sonnet → middle, opus → big
- Per-model API keys and base URLs
- Optional client API key validation
- Azure OpenAI support
- Custom headers via env vars
- Auto-cancellation on client disconnect

---

## Quick Start

### Option 1: Build from source

```bash
# Clone the project
cd claude-code-proxy-go

# Set your OpenAI API key
export OPENAI_API_KEY=sk-your-key-here

# Build the binary
go build -o claude-code-proxy ./cmd/server

# Run it
./claude-code-proxy
```

The server starts on `http://0.0.0.0:8082`.

### Option 2: Run directly (no build step)

```bash
export OPENAI_API_KEY=sk-your-key-here
go run ./cmd/server
```

### Option 3: Docker

```bash
# Build the image
docker build -t claude-code-proxy .

# Run with your API key
docker run -d \
  --name claude-proxy \
  -p 8082:8082 \
  -e OPENAI_API_KEY=sk-your-key-here \
  claude-code-proxy
```

### Option 4: Cross-compile for other platforms

```bash
# Linux
GOOS=linux GOARCH=amd64 go build -o claude-code-proxy-linux ./cmd/server

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o claude-code-proxy-mac ./cmd/server

# Windows
GOOS=windows GOARCH=amd64 go build -o claude-code-proxy.exe ./cmd/server
```

---

## Verify It Works

```bash
# Health check
curl http://localhost:8082/health

# Test OpenAI connectivity
curl http://localhost:8082/test-connection

# Send a Claude-format message
curl -X POST http://localhost:8082/v1/messages \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-3-5-sonnet-20241022",
    "max_tokens": 100,
    "messages": [
      {"role": "user", "content": "Hello!"}
    ]
  }'
```

---

## Use with Claude Code

Point Claude Code at this proxy instead of the Anthropic API:

```bash
export ANTHROPIC_BASE_URL=http://localhost:8082
export ANTHROPIC_API_KEY=dummy  # or set ANTHROPIC_API_KEY on the proxy for validation
claude
```

---

## Configuration

All settings are via environment variables. See `.env.example` for the full list.

### Using a .env file

The app reads from environment variables at startup — it does not auto-load `.env` files. To use one:

```bash
# Copy the example
cp .env.example .env

# Edit with your values
nano .env   # or vim, code, etc.

# Source it before running (set -a auto-exports all variables)
set -a && source .env && set +a

# Now run the server
./claude-code-proxy
```

### Required

| Variable | Description |
|----------|-------------|
| `OPENAI_API_KEY` | Your OpenAI (or compatible) API key |

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `HOST` | `0.0.0.0` | Listen address |
| `PORT` | `8082` | Listen port |
| `LOG_LEVEL` | `INFO` | `DEBUG`, `INFO`, `WARNING`, `ERROR` |
| `REQUEST_TIMEOUT` | `90` | Upstream timeout in seconds |

### Model Mapping

| Variable | Default | Description |
|----------|---------|-------------|
| `BIG_MODEL` | `gpt-4o` | Used for Claude opus requests |
| `MIDDLE_MODEL` | *(same as BIG_MODEL)* | Used for Claude sonnet requests |
| `SMALL_MODEL` | `gpt-4o-mini` | Used for Claude haiku requests |

### Per-Model Keys

Route different model tiers to different providers:

```bash
# Route opus to one provider, haiku to another
BIG_MODEL=gpt-4o
BIG_MODEL_API_KEY=sk-provider1-key
BIG_MODEL_BASE_URL=https://api.provider1.com/v1

SMALL_MODEL=gpt-4o-mini
SMALL_MODEL_API_KEY=sk-provider2-key
SMALL_MODEL_BASE_URL=https://api.provider2.com/v1
```

### Custom Headers

```bash
CUSTOM_HEADER_X_MY_HEADER=value
CUSTOM_HEADER_X_ANOTHER=another-value
```

These are forwarded to the upstream API on every request.

---

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/v1/messages` | Yes | Create a message (Claude → OpenAI) |
| `POST` | `/v1/messages/count_tokens` | Yes | Estimate token count |
| `GET` | `/v1/models` | Yes | List available Claude models |
| `GET` | `/health` | No | Health check |
| `GET` | `/test-connection` | No | Test upstream API connectivity |
| `GET` | `/` | No | Root info and config summary |

Auth is required only when `ANTHROPIC_API_KEY` is set.

---

## Project Structure

```
claude-code-proxy-go/
├── cmd/server/
│   └── main.go              # Entry point, routing, CORS
├── internal/
│   ├── config/
│   │   └── config.go        # Env-based configuration
│   ├── handler/
│   │   ├── handler.go       # HTTP endpoint handlers
│   │   └── middleware.go     # API key validation
│   ├── model/
│   │   ├── claude.go        # Claude API type definitions
│   │   └── constants.go     # Shared string constants
│   ├── converter/
│   │   ├── request.go       # Claude → OpenAI conversion
│   │   ├── response.go      # OpenAI → Claude conversion + SSE
│   │   └── converter_test.go
│   ├── client/
│   │   └── openai.go        # OpenAI HTTP client + cancellation
│   └── modelmanager/
│       └── modelmanager.go  # Model name mapping
├── Dockerfile
├── .env.example
├── go.mod                   # Zero dependencies
└── README.md
```

---

## Testing

```bash
# Run all tests
OPENAI_API_KEY=sk-test go test ./...

# With verbose output
OPENAI_API_KEY=sk-test go test ./... -v

# Coverage
OPENAI_API_KEY=sk-test go test -cover ./...
```

---

## License

MIT
