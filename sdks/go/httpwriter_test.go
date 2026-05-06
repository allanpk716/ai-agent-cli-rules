package agentsdk

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewHTTPWriterSetsHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewHTTPWriter(rec, "my-tool")

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/x-ndjson")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	// Verify the writer works for streaming
	_ = w.Success(map[string]string{"status": "ok"})
	_ = w.Error("something failed")

	body := rec.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSONL lines, got %d: %q", len(lines), body)
	}
}

func TestNewHTTPWriterStreaming(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewHTTPWriter(rec, "stream-tool")

	w.SetTraceID("trace-123")
	_ = w.Progress(50, "halfway")
	_ = w.Success("done")

	lines := parseJSONLines(t, rec.Body)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if lines[0]["type"] != "progress" {
		t.Errorf("line 0 type = %v, want progress", lines[0]["type"])
	}
	if lines[0]["trace_id"] != "trace-123" {
		t.Errorf("line 0 trace_id = %v, want trace-123", lines[0]["trace_id"])
	}
	if lines[1]["type"] != "result" {
		t.Errorf("line 1 type = %v, want result", lines[1]["type"])
	}
}

func TestNewHTTPWriterQuietMode(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewHTTPWriter(rec, "quiet-tool")
	w.SetQuiet(true)

	_ = w.Warning("suppressed")
	_ = w.Progress(25, "suppressed")
	_ = w.Success("visible")

	if rec.Body.Len() == 0 {
		t.Fatal("expected at least one line after Success in quiet mode")
	}
	lines := parseJSONLines(t, rec.Body)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line (quiet mode filters warning/progress), got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Errorf("type = %v, want result", lines[0]["type"])
	}
}

func TestNewHTTPWriterPanicsOnEmptyToolName(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for empty toolName, but did not panic")
		}
		msg, ok := r.(string)
		if !ok {
			t.Fatalf("panic value is not a string: %v", r)
		}
		if msg != "agentsdk: toolName must not be empty" {
			t.Errorf("panic message = %q, want %q", msg, "agentsdk: toolName must not be empty")
		}
	}()
	rec := httptest.NewRecorder()
	NewHTTPWriter(rec, "")
}

func TestNewHTTPWriterMultipleFlushes(t *testing.T) {
	rec := httptest.NewRecorder()
	w := NewHTTPWriter(rec, "multi")

	for i := 0; i < 5; i++ {
		_ = w.Progress(i*20, "working")
	}
	_ = w.Success("done")

	lines := parseJSONLines(t, rec.Body)
	if len(lines) != 6 {
		t.Fatalf("expected 6 lines (5 progress + 1 result), got %d", len(lines))
	}
	if lines[5]["type"] != "result" {
		t.Errorf("last line type = %v, want result", lines[5]["type"])
	}
}
