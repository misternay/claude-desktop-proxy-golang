package converter

import (
	"context"
	"io"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"claude-code-proxy-go/internal/model"
)

// TestStreamingIdleTimeout verifies that an upstream that stops sending SSE
// lines triggers the idle timeout: the upstream request is cancelled, a
// Claude SSE error event is written, and the converter returns instead of
// hanging forever.
func TestStreamingIdleTimeout(t *testing.T) {
	// A pipe with no writer blocks the scanner forever — simulating an
	// upstream that stalls mid-stream.
	pr, _ := io.Pipe()
	defer pr.Close()

	recorder := httptest.NewRecorder()
	cancelled := false
	cancelFn := func() { cancelled = true }

	oldTimeout := StreamIdleTimeout
	StreamIdleTimeout = 100 * time.Millisecond
	defer func() { StreamIdleTimeout = oldTimeout }()

	done := make(chan struct{})
	go func() {
		ConvertOpenAIStreamingToClaude(recorder, pr, nil, context.Background(), nil, cancelFn)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ConvertOpenAIStreamingToClaude did not return after idle timeout")
	}

	body := recorder.Body.String()
	if !strings.Contains(body, "event: error") {
		t.Errorf("expected error event on idle timeout, got:\n%s", body)
	}
	if !strings.Contains(body, "stalled") {
		t.Errorf("expected stalled message in error event, got:\n%s", body)
	}
	if !cancelled {
		t.Error("expected cancelFn to be called on idle timeout")
	}
	if strings.Contains(body, "event: message_stop") {
		t.Errorf("expected no message_stop after abort, got:\n%s", body)
	}
}

// TestStreamingIdleTimeoutHappyPath verifies a stream that finishes well
// within the idle timeout is unaffected and completes normally.
func TestStreamingIdleTimeoutHappyPath(t *testing.T) {
	stream := strings.NewReader(
		"data:{\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
			"data: [DONE]\n\n")

	recorder := httptest.NewRecorder()
	cancelled := false

	oldTimeout := StreamIdleTimeout
	StreamIdleTimeout = 5 * time.Second
	defer func() { StreamIdleTimeout = oldTimeout }()

	ConvertOpenAIStreamingToClaude(recorder, stream, &model.MessagesRequest{Model: "test"}, context.Background(), nil, func() { cancelled = true })

	body := recorder.Body.String()
	if !strings.Contains(body, `"text":"hi"`) && !strings.Contains(body, "hi") {
		t.Errorf("expected content delivered, got:\n%s", body)
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Errorf("expected normal completion message_stop, got:\n%s", body)
	}
	if cancelled {
		t.Error("cancelFn should not be called on normal completion")
	}
}

// TestStreamingReadErrorSurfaces verifies that a scanner error mid-stream
// (e.g. upstream closes the body abruptly) finishes with stop_reason "error"
// and without a bogus end_turn message_delta.
func TestStreamingReadErrorSurfaces(t *testing.T) {
	// Partial frame then a hard read error (not a clean EOF).
	stream := &errReader{
		data: []byte("data:{\"choices\":[]}\n\n"),
		err:  io.ErrUnexpectedEOF,
	}

	recorder := httptest.NewRecorder()
	ConvertOpenAIStreamingToClaude(recorder, stream, &model.MessagesRequest{Model: "test"}, context.Background(), nil, nil)
	body := recorder.Body.String()

	if !strings.Contains(body, `"stop_reason":"error"`) {
		t.Errorf("expected stop_reason error on read failure, got:\n%s", body)
	}
	// The read error must not present itself as a clean end_turn.
	if strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Errorf("expected no end_turn after read error, got:\n%s", body)
	}
}

// errReader returns err on Read after returning the given data once.
type errReader struct {
	data []byte
	err  error
	done bool
}

func (r *errReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, r.data), nil
	}
	return 0, r.err
}
