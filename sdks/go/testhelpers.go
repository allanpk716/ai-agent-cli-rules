package agentsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// NewTestApp creates an App wired to a bytes.Buffer writer for testing.
// It returns the App and the Buffer so callers can inspect JSONL output.
func NewTestApp(name, version string) (*App, *bytes.Buffer) {
	app := New(name, version)
	buf := &bytes.Buffer{}
	app.SetWriter(NewWriter(buf, name))
	return app, buf
}

// ParseEnvelope parses a single JSONL line into an Envelope.
func ParseEnvelope(line string) (Envelope, error) {
	var env Envelope
	err := json.Unmarshal([]byte(line), &env)
	return env, err
}

// ParseEnvelopes splits multi-line JSONL output into Envelope values.
// Empty lines are skipped. Returns an error on the first malformed line.
func ParseEnvelopes(output string) ([]Envelope, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var envelopes []Envelope
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		env, err := ParseEnvelope(line)
		if err != nil {
			return nil, fmt.Errorf("parse line %q: %w", line, err)
		}
		envelopes = append(envelopes, env)
	}
	return envelopes, nil
}

// CaptureOutput runs fn and returns all writer output as a string.
// This is useful for asserting JSONL output in a single call.
func CaptureOutput(app *App, fn func()) string {
	buf := &bytes.Buffer{}
	app.SetWriter(NewWriter(buf, app.Name()))
	fn()
	return buf.String()
}

// NewFakeCommand creates a minimal Cobra command for testing.
// The run function is wired to the command's Run field.
func NewFakeCommand(use string, run func(*cobra.Command, []string)) *cobra.Command {
	return &cobra.Command{Use: use, Run: run}
}

// MustParseEnvelope parses a single JSONL line and fails t if parsing fails.
func MustParseEnvelope(t *testing.T, line string) Envelope {
	t.Helper()
	env, err := ParseEnvelope(line)
	if err != nil {
		t.Fatalf("MustParseEnvelope: %v", err)
	}
	return env
}

// MustParseEnvelopes parses multi-line JSONL output and fails t on the first error.
func MustParseEnvelopes(t *testing.T, output string) []Envelope {
	t.Helper()
	envs, err := ParseEnvelopes(output)
	if err != nil {
		t.Fatalf("MustParseEnvelopes: %v", err)
	}
	return envs
}
