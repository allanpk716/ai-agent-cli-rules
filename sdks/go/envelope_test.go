package agentsdk

import (
	"encoding/json"
	"testing"
)

func TestEnvelopeVersion(t *testing.T) {
	if EnvelopeVersion != "1.0" {
		t.Errorf("EnvelopeVersion = %q, want %q", EnvelopeVersion, "1.0")
	}
}

func TestNewResultEnvelope(t *testing.T) {
	data := map[string]string{"key": "value"}
	env := NewResultEnvelope("test-tool", data)

	if env.Version != EnvelopeVersion {
		t.Errorf("Version = %q, want %q", env.Version, EnvelopeVersion)
	}
	if env.Tool != "test-tool" {
		t.Errorf("Tool = %q, want %q", env.Tool, "test-tool")
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}
	if env.Timestamp == "" {
		t.Error("Timestamp should not be empty")
	}
	if env.Data == nil {
		t.Error("Data should not be nil")
	}
	// Field exclusion: no error_code, no percent
	if env.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", env.ErrorCode)
	}
	if env.Percent != 0 {
		t.Errorf("Percent = %d, want 0", env.Percent)
	}
}

func TestNewErrorEnvelope(t *testing.T) {
	env := NewErrorEnvelope("test-tool", "NETWORK_TIMEOUT", "connection failed")

	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "NETWORK_TIMEOUT" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "NETWORK_TIMEOUT")
	}
	if env.Message != "connection failed" {
		t.Errorf("Message = %q, want %q", env.Message, "connection failed")
	}
	// Field exclusion: no data, no percent
	if env.Data != nil {
		t.Error("Data should be nil")
	}
	if env.Percent != 0 {
		t.Errorf("Percent = %d, want 0", env.Percent)
	}
}

func TestNewWarningEnvelope(t *testing.T) {
	env := NewWarningEnvelope("test-tool", "deprecated API")

	if env.Type != TypeWarning {
		t.Errorf("Type = %q, want %q", env.Type, TypeWarning)
	}
	if env.Message != "deprecated API" {
		t.Errorf("Message = %q, want %q", env.Message, "deprecated API")
	}
	// Field exclusion: no data, no error_code, no percent
	if env.Data != nil {
		t.Error("Data should be nil")
	}
	if env.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", env.ErrorCode)
	}
	if env.Percent != 0 {
		t.Errorf("Percent = %d, want 0", env.Percent)
	}
}

func TestNewProgressEnvelope(t *testing.T) {
	env := NewProgressEnvelope("test-tool", 75, "processing...")

	if env.Type != TypeProgress {
		t.Errorf("Type = %q, want %q", env.Type, TypeProgress)
	}
	if env.Percent != 75 {
		t.Errorf("Percent = %d, want 75", env.Percent)
	}
	if env.Message != "processing..." {
		t.Errorf("Message = %q, want %q", env.Message, "processing...")
	}
	// Field exclusion: no data, no error_code
	if env.Data != nil {
		t.Error("Data should be nil")
	}
	if env.ErrorCode != "" {
		t.Errorf("ErrorCode = %q, want empty", env.ErrorCode)
	}
}

func TestEnvelopeFieldExclusionJSON(t *testing.T) {
	// Verify constructors produce JSON that omits excluded fields.
	tests := []struct {
		name string
		env  Envelope
	}{
		{"result", NewResultEnvelope("t", map[string]string{"k": "v"})},
		{"error", NewErrorEnvelope("t", "CODE", "msg")},
		{"warning", NewWarningEnvelope("t", "msg")},
		{"progress", NewProgressEnvelope("t", 50, "msg")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := json.Marshal(tt.env)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			var m map[string]interface{}
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}

			switch tt.name {
			case "result":
				if _, ok := m["data"]; !ok {
					t.Error("result envelope missing data field in JSON")
				}
				if _, ok := m["error_code"]; ok {
					t.Error("result envelope should not have error_code in JSON")
				}
				if _, ok := m["percent"]; ok {
					t.Error("result envelope should not have percent in JSON")
				}
			case "error":
				if _, ok := m["error_code"]; !ok {
					t.Error("error envelope missing error_code field in JSON")
				}
				if _, ok := m["data"]; ok {
					t.Error("error envelope should not have data in JSON")
				}
				if _, ok := m["percent"]; ok {
					t.Error("error envelope should not have percent in JSON")
				}
			case "warning":
				if _, ok := m["message"]; !ok {
					t.Error("warning envelope missing message field in JSON")
				}
				if _, ok := m["data"]; ok {
					t.Error("warning envelope should not have data in JSON")
				}
				if _, ok := m["error_code"]; ok {
					t.Error("warning envelope should not have error_code in JSON")
				}
				if _, ok := m["percent"]; ok {
					t.Error("warning envelope should not have percent in JSON")
				}
			case "progress":
				if _, ok := m["percent"]; !ok {
					t.Error("progress envelope missing percent field in JSON")
				}
				if _, ok := m["data"]; ok {
					t.Error("progress envelope should not have data in JSON")
				}
				if _, ok := m["error_code"]; ok {
					t.Error("progress envelope should not have error_code in JSON")
				}
			}
		})
	}
}

func TestEnvelopeTimestampFormat(t *testing.T) {
	env := NewResultEnvelope("t", "data")
	// Verify timestamp is valid RFC3339
	if len(env.Timestamp) < 20 {
		t.Errorf("Timestamp %q looks too short for RFC3339", env.Timestamp)
	}
	// Should end with Z or have timezone offset
	last := env.Timestamp[len(env.Timestamp)-1]
	if last != 'Z' && last != '0' && last != '9' {
		t.Errorf("Timestamp %q doesn't look like RFC3339", env.Timestamp)
	}
}
