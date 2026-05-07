package agentsdk

import "time"

// EnvelopeVersion is the protocol version for all envelopes.
const EnvelopeVersion = "1.0"

// Envelope type constants.
const (
	TypeResult   = "result"
	TypeError    = "error"
	TypeWarning  = "warning"
	TypeProgress = "progress"
)

// Envelope is the top-level JSONL wrapper. Field exclusion is enforced via
// constructors — zero-value fields with omitempty are omitted from JSON output.
type Envelope struct {
	Version   string      `json:"version"`
	Tool      string      `json:"tool"`
	Type      string      `json:"type"`
	Timestamp string      `json:"timestamp"`
	Data      interface{} `json:"data,omitempty"`
	ErrorCode string      `json:"error_code,omitempty"`
	Message   string      `json:"message,omitempty"`
	Percent   int         `json:"percent,omitempty"`
	TraceID   string      `json:"trace_id,omitempty"`
	Kind      string      `json:"kind,omitempty"`
}

// NewResultEnvelope creates a result envelope with data only.
// Fields error_code and percent are guaranteed zero-valued (omitted from JSON).
// When kind is non-empty, a top-level "kind" field is included in the JSON output.
func NewResultEnvelope(tool string, data interface{}, kind ...string) Envelope {
	var k string
	if len(kind) > 0 && kind[0] != "" {
		k = kind[0]
	}
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      tool,
		Type:      TypeResult,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
		Kind:      k,
	}
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      tool,
		Type:      TypeResult,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Data:      data,
	}
}

// NewErrorEnvelope creates an error envelope with error_code and message only.
// Fields data and percent are guaranteed zero-valued (omitted from JSON).
func NewErrorEnvelope(tool string, errorCode string, message string) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      tool,
		Type:      TypeError,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		ErrorCode: errorCode,
		Message:   message,
	}
}

// NewWarningEnvelope creates a warning envelope with message only.
// Fields data, error_code and percent are guaranteed zero-valued (omitted from JSON).
func NewWarningEnvelope(tool string, message string) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      tool,
		Type:      TypeWarning,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Message:   message,
	}
}

// NewProgressEnvelope creates a progress envelope with percent and optional message.
// Fields data and error_code are guaranteed zero-valued (omitted from JSON).
func NewProgressEnvelope(tool string, percent int, message string) Envelope {
	return Envelope{
		Version:   EnvelopeVersion,
		Tool:      tool,
		Type:      TypeProgress,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Percent:   percent,
		Message:   message,
	}
}
