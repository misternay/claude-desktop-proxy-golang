package client

import (
	"testing"
)

// TestOpenAIError_ClaudeError verifies the authoritative upstream-status →
// (HTTP status, Claude error type) mapping. Pure function -> fully parallel,
// table-driven with named subtests.
func TestOpenAIError_ClaudeError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		statusCode int
		wantStatus int
		wantType   string
	}{
		{name: "400 maps to 400 invalid_request_error", statusCode: 400, wantStatus: 400, wantType: "invalid_request_error"},
		{name: "401 maps to 401 authentication_error", statusCode: 401, wantStatus: 401, wantType: "authentication_error"},
		{name: "403 maps to 401 authentication_error", statusCode: 403, wantStatus: 401, wantType: "authentication_error"},
		{name: "404 maps to 404 not_found_error", statusCode: 404, wantStatus: 404, wantType: "not_found_error"},
		{name: "405 maps to 502 api_error", statusCode: 405, wantStatus: 502, wantType: "api_error"},
		{name: "409 maps to 502 api_error", statusCode: 409, wantStatus: 502, wantType: "api_error"},
		{name: "429 maps to 429 rate_limit_error", statusCode: 429, wantStatus: 429, wantType: "rate_limit_error"},
		{name: "500 maps to 502 api_error", statusCode: 500, wantStatus: 502, wantType: "api_error"},
		{name: "502 maps to 502 api_error", statusCode: 502, wantStatus: 502, wantType: "api_error"},
		{name: "503 maps to 502 api_error", statusCode: 503, wantStatus: 502, wantType: "api_error"},
		{name: "504 maps to 502 api_error", statusCode: 504, wantStatus: 502, wantType: "api_error"},
		// Network-level failures never got an HTTP status (StatusCode 0).
		{name: "zero status maps to 502 api_error", statusCode: 0, wantStatus: 502, wantType: "api_error"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			e := &OpenAIError{StatusCode: tc.statusCode, Message: "boom", Type: "unknown"}
			gotStatus, gotType := e.ClaudeError()
			if gotStatus != tc.wantStatus || gotType != tc.wantType {
				t.Errorf("ClaudeError() for status %d: got (%d, %q), want (%d, %q)",
					tc.statusCode, gotStatus, gotType, tc.wantStatus, tc.wantType)
			}
		})
	}
}
