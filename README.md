# Claude Code Proxy (Go)

> This project is a rewrite of [9homme/claude-code-proxy](https://github.com/9homme/claude-code-proxy) for personal use only, not for commercial use.

A high-performance proxy server that accepts **Claude API** format requests and forwards them to **OpenAI-compatible** API providers. Written in Go with a minimal dependency footprint (stdlib + `yaml.v3` for config).

## Why Go?

- **8.9 MB** static binary — no Python runtime needed
- **~2,500 lines** of clean, idiomatic Go
- **Minimal dependencies** — builds with just `go build`
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
- Custom upstream headers via `config.yaml`
- Auto-cancellation on client disconnect

---

## Quick Start

### Option 1: Build from source

```bash
# Clone the project
cd claude-code-proxy-go

# Create your config file from the template
cp config.example.yaml config.yaml
# Edit config.yaml and set openai_api_key
$EDITOR config.yaml

# Build the binary
go build -o claude-code-proxy ./cmd/server

# Run it
./claude-code-proxy
```

The server starts on `http://0.0.0.0:8082`.

### Option 2: Run directly (no build step)

```bash
cp config.example.yaml config.yaml   # then edit and set openai_api_key
go run ./cmd/server
```

### Option 3: Docker

```bash
# Build the image
docker build -t claude-code-proxy .

# Run, mounting your config at the working directory
docker run -d \
  --name claude-proxy \
  -p 8082:8082 \
  -v ./config.yaml:/config.yaml:ro \
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
ANTHROPIC_BASE_URL=http://localhost:8082 claude
```

---

## Configuration

All settings live in a single `config.yaml`. The proxy searches, in order:

1. Path given to `--config <path>` (e.g. `./claude-code-proxy --config /opt/prod/config.yaml`)
2. `./config.yaml` — the current working directory

If no config file is found, the proxy exits with an error. See [`config.example.yaml`](./config.example.yaml) for the full annotated schema.

> Numeric values use explicit-zero semantics: setting `port: 0` (or `max_retries: 0`, etc.) is honored as the literal zero, not replaced by the default. Omit the key entirely to get the default.

### Minimal config

```yaml
openai_api_key: sk-your-key-here
```

### Required

| Key | Description |
|----------|-------------|
| `openai_api_key` | Your OpenAI (or compatible) API key |

### Security

| Key | Default | Description |
|----------|---------|-------------|
| `anthropic_api_key` | _(empty)_ | If set, clients must send this key to access the proxy. If empty, the proxy is open access. |

### Upstream

| Key | Default | Description |
|----------|---------|-------------|
| `openai_base_url` | `https://api.openai.com/v1` | OpenAI-compatible API endpoint |
| `azure_api_version` | _(empty)_ | Azure OpenAI deployment API version (leave empty for non-Azure) |

### Server

| Key | Default | Description |
|----------|---------|-------------|
| `host` | `0.0.0.0` | Listen address |
| `port` | `8082` | Listen port |
| `log_level` | `INFO` | `DEBUG`, `INFO`, `WARNING`, `ERROR` |
| `request_timeout` | `90` | Upstream timeout in seconds |
| `max_retries` | `2` | Max retry attempts |

### Token limits

| Key | Default | Description |
|----------|---------|-------------|
| `max_tokens_limit` | `4096` | Upper clamp for requested `max_tokens` |
| `min_tokens_limit` | `100` | Lower clamp for requested `max_tokens` |
| `max_tokens` | _(= max_tokens_limit)_ | Override the upper clamp |
| `min_tokens` | _(= min_tokens_limit)_ | Override the lower clamp |

### Model mapping

```yaml
models:
  big:
    name: gpt-4o          # used for Claude opus requests
  middle:
    name: gpt-4o          # leave empty to inherit big.name; used for sonnet
  small:
    name: gpt-4o-mini     # used for haiku
```

### Per-model keys & base URLs

Route different model tiers to different providers. Empty `api_key` and `base_url`
fall back to the top-level `openai_api_key` / `openai_base_url`:

```yaml
models:
  big:
    name: gpt-4o
    api_key: sk-provider1-key
    base_url: https://api.provider1.com/v1
  small:
    name: gpt-4o-mini
    api_key: sk-provider2-key
    base_url: https://api.provider2.com/v1
```

### Custom headers

```yaml
custom_headers:
  X-My-Header: value
  X-Another: another-value
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

Auth is required only when `anthropic_api_key` is set in `config.yaml`; otherwise the proxy is open access. Authenticated endpoints also pass through a per-IP rate limiter (100 requests/minute).

---

## Project Structure

```
claude-code-proxy-go/
├── cmd/server/
│   └── main.go              # Entry point, routing, CORS, --help/--config flags
├── internal/
│   ├── config/
│   │   ├── config.go        # YAML config loading + fallback defaults
│   │   └── config_test.go
│   ├── handler/
│   │   ├── handler.go       # HTTP endpoint handlers
│   │   ├── middleware.go     # API key validation
│   │   └── ratelimit.go     # Per-IP rate limiter
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
├── config.example.yaml      # Annotated config template
├── Dockerfile
├── go.mod                   # stdlib + gopkg.in/yaml.v3
└── README.md
```

---

## Testing

Tests are hermetic — they self-seed a default `config.AppConfig` via `TestMain`, so no config file or environment variables are required.

```bash
# Run all tests
go test ./...

# With verbose output
go test ./... -v

# With race detector
go test -race ./...

# Coverage
go test -cover ./...
```

---

## License

MIT
