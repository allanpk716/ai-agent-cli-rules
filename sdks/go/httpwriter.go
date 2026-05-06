package agentsdk

import (
	"net/http"
)

// NewHTTPWriter creates a Writer that streams JSONL envelopes to an HTTP
// ResponseWriter.  It sets Content-Type to application/x-ndjson, writes a
// 200 status header, and returns a Writer wrapping w.
// Panics if toolName is empty.
func NewHTTPWriter(w http.ResponseWriter, toolName string) *Writer {
	if toolName == "" {
		panic("agentsdk: toolName must not be empty")
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	return NewWriter(w, toolName)
}
