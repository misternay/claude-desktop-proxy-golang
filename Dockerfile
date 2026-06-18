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

# Configuration is read from config.yaml. Mount your config at the default
# discovery path so no extra flags are needed:
#   docker run -v ./config.yaml:/etc/claude-code-proxy/config.yaml ...
# Default search order: /etc/claude-code-proxy/config.yaml is used here.
CMD ["/claude-code-proxy"]
