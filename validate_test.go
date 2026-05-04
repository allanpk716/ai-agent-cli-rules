package agentsdk

import (
	"testing"
	"time"
)

func makeValidEnvelope(envType string) Envelope {
	base := Envelope{
		Version:   EnvelopeVersion,
		Tool:      "test-tool",
		Type:      envType,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	switch envType {
	case TypeResult:
		base.Data = map[string]string{"key": "value"}
	case TypeError:
		base.ErrorCode = "SOME_ERROR"
		base.Message = "something went wrong"
	case TypeWarning:
		base.Message = "a warning"
	case TypeProgress:
		base.Percent = 50
		base.Message = "halfway"
	}
	return base
}

func TestValidateEnvelopeValid(t *testing.T) {
	types := []string{TypeResult, TypeError, TypeWarning, TypeProgress}
	for _, envType := range types {
		t.Run(envType, func(t *testing.T) {
			env := makeValidEnvelope(envType)
			if err := ValidateEnvelope(env); err != nil {
				t.Errorf("expected valid, got error: %v", err)
			}
		})
	}
}

func TestValidateEnvelopeMissingFields(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(Envelope) Envelope
		wantErr string
	}{
		{
			"missing_version",
			func(e Envelope) Envelope { e.Version = ""; return e },
			"missing version",
		},
		{
			"missing_tool",
			func(e Envelope) Envelope { e.Tool = ""; return e },
			"missing tool",
		},
		{
			"missing_type",
			func(e Envelope) Envelope { e.Type = ""; return e },
			"missing type",
		},
		{
			"missing_timestamp",
			func(e Envelope) Envelope { e.Timestamp = ""; return e },
			"missing timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := tt.modify(makeValidEnvelope(TypeResult))
			err := ValidateEnvelope(env)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
		})
	}
}

func TestValidateEnvelopeInvalidType(t *testing.T) {
	env := makeValidEnvelope(TypeResult)
	env.Type = "unknown"
	err := ValidateEnvelope(env)
	if err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestValidateEnvelopeResultFieldExclusion(t *testing.T) {
	// Result with error_code should fail
	env := makeValidEnvelope(TypeResult)
	env.ErrorCode = "SHOULD_NOT_BE_HERE"
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for result with error_code")
	}

	// Result with percent should fail
	env = makeValidEnvelope(TypeResult)
	env.Percent = 50
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for result with percent")
	}

	// Result without data should fail
	env = makeValidEnvelope(TypeResult)
	env.Data = nil
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for result without data")
	}
}

func TestValidateEnvelopeErrorFieldExclusion(t *testing.T) {
	// Error without error_code should fail
	env := makeValidEnvelope(TypeError)
	env.ErrorCode = ""
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for error envelope without error_code")
	}

	// Error without message should fail
	env = makeValidEnvelope(TypeError)
	env.Message = ""
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for error envelope without message")
	}

	// Error with data should fail
	env = makeValidEnvelope(TypeError)
	env.Data = map[string]string{"bad": "field"}
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for error envelope with data")
	}

	// Error with percent should fail
	env = makeValidEnvelope(TypeError)
	env.Percent = 50
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for error envelope with percent")
	}
}

func TestValidateEnvelopeWarningFieldExclusion(t *testing.T) {
	// Warning without message should fail
	env := makeValidEnvelope(TypeWarning)
	env.Message = ""
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for warning without message")
	}

	// Warning with data should fail
	env = makeValidEnvelope(TypeWarning)
	env.Data = "bad"
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for warning with data")
	}

	// Warning with error_code should fail
	env = makeValidEnvelope(TypeWarning)
	env.ErrorCode = "BAD"
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for warning with error_code")
	}
}

func TestValidateEnvelopeProgressFieldExclusion(t *testing.T) {
	// Progress with negative percent should fail
	env := makeValidEnvelope(TypeProgress)
	env.Percent = -1
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for progress with negative percent")
	}

	// Progress with percent > 100 should fail
	env = makeValidEnvelope(TypeProgress)
	env.Percent = 101
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for progress with percent > 100")
	}

	// Progress with data should fail
	env = makeValidEnvelope(TypeProgress)
	env.Data = "bad"
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for progress with data")
	}

	// Progress with error_code should fail
	env = makeValidEnvelope(TypeProgress)
	env.ErrorCode = "BAD"
	if err := ValidateEnvelope(env); err == nil {
		t.Error("expected error for progress with error_code")
	}
}

func TestValidateEnvelopeProgressZeroPercent(t *testing.T) {
	// 0 is a valid percent
	env := makeValidEnvelope(TypeProgress)
	env.Percent = 0
	env.Message = "starting"
	if err := ValidateEnvelope(env); err != nil {
		t.Errorf("expected valid for progress with percent=0, got: %v", err)
	}
}

func TestValidateEnvelopeProgress100Percent(t *testing.T) {
	// 100 is valid
	env := makeValidEnvelope(TypeProgress)
	env.Percent = 100
	env.Message = "done"
	if err := ValidateEnvelope(env); err != nil {
		t.Errorf("expected valid for progress with percent=100, got: %v", err)
	}
}
