package agentsdk

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func parseJSONLines(t *testing.T, buf *bytes.Buffer) []map[string]interface{} {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	result := make([]map[string]interface{}, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON line: %q, err: %v", line, err)
		}
		result = append(result, m)
	}
	return result
}

func TestWriterSuccess(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")

	err := w.Success(map[string]string{"id": "42"})
	if err != nil {
		t.Fatalf("Success() error: %v", err)
	}

	lines := parseJSONLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["type"] != "result" {
		t.Errorf("type = %v, want result", lines[0]["type"])
	}
	if lines[0]["tool"] != "my-tool" {
		t.Errorf("tool = %v, want my-tool", lines[0]["tool"])
	}
	data, ok := lines[0]["data"].(map[string]interface{})
	if !ok {
		t.Fatal("data is not a map")
	}
	if data["id"] != "42" {
		t.Errorf("data.id = %v, want 42", data["id"])
	}
}

func TestWriterErrorWithCode(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")

	err := w.ErrorWithCode("NETWORK_TIMEOUT", "conn failed")
	if err != nil {
		t.Fatalf("ErrorWithCode() error: %v", err)
	}

	lines := parseJSONLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if lines[0]["type"] != "error" {
		t.Errorf("type = %v, want error", lines[0]["type"])
	}
	if lines[0]["error_code"] != "NETWORK_TIMEOUT" {
		t.Errorf("error_code = %v, want NETWORK_TIMEOUT", lines[0]["error_code"])
	}
}

func TestWriterErrorDefaultCode(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")

	err := w.Error("something went wrong")
	if err != nil {
		t.Fatalf("Error() error: %v", err)
	}

	lines := parseJSONLines(t, &buf)
	if lines[0]["error_code"] != "error" {
		t.Errorf("error_code = %v, want error", lines[0]["error_code"])
	}
}

func TestWriterQuietFilter(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")
	w.SetQuiet(true)

	// Warning and Progress should be suppressed
	_ = w.Warning("should be suppressed")
	_ = w.Progress(50, "should be suppressed too")

	if buf.Len() != 0 {
		t.Errorf("quiet mode should suppress warning/progress, got output: %q", buf.String())
	}

	// Result and Error should still produce output
	_ = w.Success("ok")
	_ = w.Error("fail")

	lines := parseJSONLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (result + error), got %d: %v", len(lines), lines)
	}
	if lines[0]["type"] != "result" {
		t.Errorf("line 0 type = %v, want result", lines[0]["type"])
	}
	if lines[1]["type"] != "error" {
		t.Errorf("line 1 type = %v, want error", lines[1]["type"])
	}
}

func TestWriterQuietOffProducesAll(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")
	w.SetQuiet(false)

	_ = w.Success("ok")
	_ = w.Warning("warn")
	_ = w.Progress(50, "halfway")
	_ = w.Error("fail")

	lines := parseJSONLines(t, &buf)
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines, got %d", len(lines))
	}
}

func TestWriterTraceIDInjection(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")
	w.SetTraceID("trace-abc-123")

	_ = w.Success("data")
	_ = w.Error("fail")

	lines := parseJSONLines(t, &buf)
	for i, line := range lines {
		if line["trace_id"] != "trace-abc-123" {
			t.Errorf("line %d: trace_id = %v, want trace-abc-123", i, line["trace_id"])
		}
	}
}

func TestWriterEmptyTraceIDOmitted(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")
	// No trace ID set (default empty string)

	_ = w.Success("data")

	lines := parseJSONLines(t, &buf)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if _, ok := lines[0]["trace_id"]; ok {
		t.Error("trace_id should be omitted when empty, but present in JSON output")
	}
}

func TestWriterSetTraceIDMidStream(t *testing.T) {
	var buf bytes.Buffer
	w := NewWriter(&buf, "my-tool")

	_ = w.Success("before")
	w.SetTraceID("trace-xyz")
	_ = w.Success("after")

	lines := parseJSONLines(t, &buf)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	if _, ok := lines[0]["trace_id"]; ok {
		t.Error("first line should not have trace_id")
	}
	if lines[1]["trace_id"] != "trace-xyz" {
		t.Errorf("second line trace_id = %v, want trace-xyz", lines[1]["trace_id"])
	}
}

func TestNewWriterPanicsOnEmptyToolName(t *testing.T) {
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
	var buf bytes.Buffer
	NewWriter(&buf, "")
}
