package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-code-proxy-go/internal/client"
)

// TestMaxBytesReader rejects oversized bodies with 413.
func TestMaxBytesReader(t *testing.T) {
	// A syntactically VALID JSON body whose size exceeds the limit: a JSON
	// string of maxRequestBodyBytes+1 characters. (Invalid JSON fails fast
	// with a syntax error before the size limit is hit.)
	big := `"` + strings.Repeat("a", maxRequestBodyBytes+1) + `"`

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(big)))

	got := decodeJSONBody(rr, req, &json.RawMessage{})
	if got {
		t.Fatal("expected decodeJSONBody to fail on oversized body")
	}
	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 413, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["type"] != "error" {
		t.Errorf("expected error body, got %v", body)
	}
	errObj := body["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf("expected invalid_request_error type, got %v", errObj["type"])
	}
}

// TestMalformedBody returns 400 on invalid JSON.
func TestMalformedBody(t *testing.T) {
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader([]byte(`{"invalid json`)))

	got := decodeJSONBody(rr, req, &json.RawMessage{})
	if got {
		t.Fatal("expected decodeJSONBody to fail on malformed body")
	}
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// TestWriteUpstreamError_RetryAfter verifies Retry-After is forwarded on 429.
func TestWriteUpstreamError_RetryAfter(t *testing.T) {
	rr := httptest.NewRecorder()

	err := &client.OpenAIError{
		StatusCode: http.StatusTooManyRequests,
		Message:    "slow down",
		Type:       "rate_limit",
		Headers:    http.Header{"Retry-After": []string{"12"}},
	}

	writeUpstreamError(rr, "test", err)

	if rr.Code != http.StatusTooManyRequests {
		t.Errorf("expected 429, got %d", rr.Code)
	}
	if rr.Header().Get("Retry-After") != "12" {
		t.Errorf("expected Retry-After 12 forwarded, got %q", rr.Header().Get("Retry-After"))
	}
}

// TestWriteUpstreamError_Permission verifies 403 maps to permission_error.
func TestWriteUpstreamError_Permission(t *testing.T) {
	rr := httptest.NewRecorder()

	err := &client.OpenAIError{
		StatusCode: http.StatusForbidden,
		Message:    "no access",
		Type:       "permission",
	}

	writeUpstreamError(rr, "test", err)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	errObj := body["error"].(map[string]any)
	if errObj["type"] != "permission_error" {
		t.Errorf("expected permission_error type, got %v", errObj["type"])
	}
}
