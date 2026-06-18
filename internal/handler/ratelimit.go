package handler

import (
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// RateLimiter implements a fixed-window rate limiter keyed by client IP.
type RateLimiter struct {
	mu          sync.Mutex
	requests    map[string]*bucket
	rate        int           // requests allowed per window
	window      time.Duration // length of each window
	cleanupTick time.Duration // how often to evict stale buckets
	done        chan struct{} // closed by Stop() to terminate the cleanup goroutine
}

type bucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a new rate limiter and starts its background cleanup goroutine.
// Call Stop() when the limiter is no longer needed to release the goroutine.
func NewRateLimiter(rate int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests:    make(map[string]*bucket),
		rate:        rate,
		window:      window,
		cleanupTick: window * 2, // cleanup every 2 windows
		done:        make(chan struct{}),
	}

	go rl.cleanup()

	return rl
}

// Stop terminates the background cleanup goroutine. Safe to call multiple times.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.done:
		// already stopped
	default:
		close(rl.done)
	}
}

// Allow reports whether a request from the given key is within the rate limit.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	b, exists := rl.requests[key]

	if !exists {
		rl.requests[key] = &bucket{
			tokens:    rl.rate - 1,
			lastReset: now,
		}
		return true
	}

	// Reset bucket if the window has elapsed.
	if now.Sub(b.lastReset) >= rl.window {
		b.tokens = rl.rate - 1
		b.lastReset = now
		return true
	}

	if b.tokens > 0 {
		b.tokens--
		return true
	}

	return false
}

// cleanup periodically removes stale buckets to prevent unbounded memory growth.
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(rl.cleanupTick)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			rl.mu.Lock()
			now := time.Now()
			for key, b := range rl.requests {
				if now.Sub(b.lastReset) >= rl.window*2 {
					delete(rl.requests, key)
				}
			}
			rl.mu.Unlock()
		case <-rl.done:
			return
		}
	}
}

// clientIP extracts the real client IP from a request.
// It prefers X-Real-IP, then the first value of X-Forwarded-For, then RemoteAddr.
func clientIP(r *http.Request) string {
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	if ip := r.Header.Get("X-Forwarded-For"); ip != "" {
		// X-Forwarded-For may be "client, proxy1, proxy2"; take the first entry.
		for i := 0; i < len(ip); i++ {
			if ip[i] == ',' {
				return ip[:i]
			}
		}
		return ip
	}
	// RemoteAddr is "host:port"; strip the port.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimitMiddleware returns a middleware that enforces per-IP rate limits.
func RateLimitMiddleware(limiter *RateLimiter) func(http.HandlerFunc) http.HandlerFunc {
	windowSecs := strconv.Itoa(int(limiter.window.Seconds()))
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ip := clientIP(r)

			if !limiter.Allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", windowSecs)
				w.WriteHeader(http.StatusTooManyRequests)
				if err := json.NewEncoder(w).Encode(map[string]any{
					"type": "error",
					"error": map[string]any{
						"type":    "rate_limit_error",
						"message": "Rate limit exceeded. Please try again later.",
					},
				}); err != nil {
					slog.Warn("failed to encode rate limit response", "err", err)
				}
				return
			}

			next(w, r)
		}
	}
}
