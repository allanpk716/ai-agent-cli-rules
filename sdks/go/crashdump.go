package agentsdk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CrashDump represents the structured JSON data written to disk when the
// application crashes due to a signal or panic. Each field captures a
// different dimension of the crash scene for post-mortem debugging.
type CrashDump struct {
	// Timestamp is the crash time in RFC3339 format.
	Timestamp string `json:"timestamp"`

	// AppName is the name of the application that crashed.
	AppName string `json:"app_name"`

	// AppVersion is the version of the application that crashed.
	AppVersion string `json:"app_version"`

	// TraceID is the distributed trace ID active at crash time, if any.
	TraceID string `json:"trace_id,omitempty"`

	// CrashType is either "signal" or "panic".
	CrashType string `json:"crash_type"`

	// Signal is the OS signal name (e.g. "SIGTERM", "SIGINT").
	// Empty for panic-type crashes.
	Signal string `json:"signal,omitempty"`

	// PanicValue is the string representation of the panic value.
	// Empty for signal-type crashes.
	PanicValue string `json:"panic_value,omitempty"`

	// StackTrace contains the goroutine stack trace at crash time.
	StackTrace string `json:"stack_trace"`

	// FlightContext holds a snapshot of in-flight state at crash time.
	FlightContext map[string]interface{} `json:"flight_context"`
}

// WriteCrashDump atomically writes a CrashDump as formatted JSON to the
// crash_dumps/ directory managed by the Sandbox.
//
// The write uses the same write-to-tmp-then-rename pattern as ConfigManager
// to avoid partial files on crash. The filename format is crash-YYYYMMDD-HHMMSS.json
// for easy chronological sorting.
//
// If the crash_dumps/ directory does not exist, it is created via os.MkdirAll.
// Write failures are non-fatal — the function returns the error but does not
// panic, allowing callers to log and continue their shutdown sequence.
func WriteCrashDump(sandbox *Sandbox, dump *CrashDump) error {
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return fmt.Errorf("crashdump: marshal: %w", err)
	}

	dir := sandbox.CrashDumpsDir()
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("crashdump: create dir %q: %w", dir, err)
	}

	filename := fmt.Sprintf("crash-%s.json", time.Now().Format("20060102-150405"))
	finalPath := filepath.Join(dir, filename)
	tmpPath := finalPath + ".tmp"

	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return fmt.Errorf("crashdump: write tmp %q: %w", tmpPath, err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		return fmt.Errorf("crashdump: rename %q → %q: %w", tmpPath, finalPath, err)
	}

	return nil
}
