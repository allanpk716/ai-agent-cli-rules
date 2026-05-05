package agentsdk

import (
	"encoding/json"
	"io"
)

// Writer emits JSONL envelopes to an io.Writer with quiet filtering and trace ID injection.
type Writer struct {
	out      io.Writer
	toolName string
	quiet    bool
	traceID  string
}

// NewWriter creates a Writer that emits JSONL lines to out.
func NewWriter(out io.Writer, toolName string) *Writer {
	return &Writer{
		out:      out,
		toolName: toolName,
	}
}

// TraceID returns the current trace ID, or empty string if none is set.
func (w *Writer) TraceID() string {
	return w.traceID
}

// SetQuiet enables or disables quiet mode.
// In quiet mode, progress and warning envelopes are silently dropped;
// result and error envelopes are always emitted.
func (w *Writer) SetQuiet(q bool) {
	w.quiet = q
}

// SetTraceID sets the trace ID injected into all subsequent envelopes.
func (w *Writer) SetTraceID(id string) {
	w.traceID = id
}

// Success emits a result envelope with the given data.
func (w *Writer) Success(data interface{}) error {
	return w.emit(NewResultEnvelope(w.toolName, data))
}

// ErrorWithCode emits an error envelope with the given error code and message.
func (w *Writer) ErrorWithCode(code string, message string) error {
	return w.emit(NewErrorEnvelope(w.toolName, code, message))
}

// Error emits an error envelope with code "error".
func (w *Writer) Error(message string) error {
	return w.emit(NewErrorEnvelope(w.toolName, "error", message))
}

// Warning emits a warning envelope (filtered in quiet mode).
func (w *Writer) Warning(message string) error {
	return w.emit(NewWarningEnvelope(w.toolName, message))
}

// Progress emits a progress envelope (filtered in quiet mode).
func (w *Writer) Progress(percent int, message string) error {
	return w.emit(NewProgressEnvelope(w.toolName, percent, message))
}

// emit is the single bottleneck for all envelope output.
// It applies quiet filtering and trace ID injection.
func (w *Writer) emit(env Envelope) error {
	// Quiet filter: suppress progress and warning in quiet mode.
	if w.quiet && (env.Type == TypeProgress || env.Type == TypeWarning) {
		return nil
	}

	// Inject trace ID if set.
	if w.traceID != "" {
		env.TraceID = w.traceID
	}

	line, err := json.Marshal(env)
	if err != nil {
		return err
	}

	_, err = w.out.Write(append(line, '\n'))
	return err
}
