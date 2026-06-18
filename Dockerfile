FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /claude-code-proxy ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates
COPY --from=builder /claude-code-proxy /claude-code-proxy
EXPOSE 8082

# Configuration is read from config.yaml in the current working directory.
# Mount your config at /config.yaml (the container's default CWD is /):
#   docker run -v ./config.yaml:/config.yaml ...
# Or pass an explicit path:
#   docker run -v ./config.yaml:/opt/cfg.yaml ... /claude-code-proxy --config /opt/cfg.yaml
CMD ["/claude-code-proxy"]
