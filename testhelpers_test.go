package agentsdk

import (
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestNewTestApp(t *testing.T) {
	app, buf := NewTestApp("test-tool", "1.0.0")

	if app.Name() != "test-tool" {
		t.Errorf("Name() = %q, want %q", app.Name(), "test-tool")
	}
	if app.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want %q", app.Version(), "1.0.0")
	}

	// Verify buffer captures output through the writer.
	app.JSONL().Success(map[string]string{"ok": "true"})
	if buf.Len() == 0 {
		t.Error("buffer is empty — writer not wired correctly")
	}

	env := MustParseEnvelope(t, strings.TrimSpace(buf.String()))
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}
}

func TestParseEnvelope(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		line := `{"version":"1.0","tool":"t","type":"result","timestamp":"2025-01-01T00:00:00Z","data":{"k":"v"}}`
		env, err := ParseEnvelope(line)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if env.Type != TypeResult {
			t.Errorf("Type = %q, want %q", env.Type, TypeResult)
		}
		if env.Tool != "t" {
			t.Errorf("Tool = %q, want %q", env.Tool, "t")
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		_, err := ParseEnvelope("not-json")
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("empty string", func(t *testing.T) {
		_, err := ParseEnvelope("")
		if err == nil {
			t.Error("expected error for empty string, got nil")
		}
	})
}

func TestParseEnvelopes(t *testing.T) {
	t.Run("multi-line", func(t *testing.T) {
		app, buf := NewTestApp("tool", "1.0")
		app.JSONL().Success("first")
		app.JSONL().Success("second")
		app.JSONL().Success("third")

		envs, err := ParseEnvelopes(buf.String())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(envs) != 3 {
			t.Fatalf("got %d envelopes, want 3", len(envs))
		}
		for i, env := range envs {
			if env.Type != TypeResult {
				t.Errorf("envelope[%d].Type = %q, want %q", i, env.Type, TypeResult)
			}
		}
	})

	t.Run("mixed valid invalid", func(t *testing.T) {
		input := `{"version":"1.0","tool":"t","type":"result","timestamp":"2025-01-01T00:00:00Z","data":null}
BROKEN_LINE`
		_, err := ParseEnvelopes(input)
		if err == nil {
			t.Error("expected error for mixed valid/invalid input, got nil")
		}
	})

	t.Run("empty", func(t *testing.T) {
		envs, err := ParseEnvelopes("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(envs) != 0 {
			t.Errorf("got %d envelopes, want 0 for empty input", len(envs))
		}
	})

	t.Run("whitespace only", func(t *testing.T) {
		envs, err := ParseEnvelopes("  \n  \n  ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(envs) != 0 {
			t.Errorf("got %d envelopes, want 0 for whitespace-only input", len(envs))
		}
	})
}

func TestCaptureOutput(t *testing.T) {
	app, _ := NewTestApp("cap-tool", "1.0")

	output := CaptureOutput(app, func() {
		app.JSONL().Success("captured")
		app.JSONL().Progress(50, "halfway")
	})

	envs, err := ParseEnvelopes(output)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(envs))
	}
	if envs[0].Type != TypeResult {
		t.Errorf("envelope[0].Type = %q, want %q", envs[0].Type, TypeResult)
	}
	if envs[1].Type != TypeProgress {
		t.Errorf("envelope[1].Type = %q, want %q", envs[1].Type, TypeProgress)
	}
}

func TestNewFakeCommand(t *testing.T) {
	called := false
	cmd := NewFakeCommand("test-cmd", func(cmd *cobra.Command, args []string) {
		called = true
	})

	if cmd.Use != "test-cmd" {
		t.Errorf("Use = %q, want %q", cmd.Use, "test-cmd")
	}

	// Execute the command directly.
	cmd.SetArgs([]string{})
	cmd.Run(cmd, []string{})

	if !called {
		t.Error("run function was not called")
	}
}

func TestMustParseEnvelope(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		line := `{"version":"1.0","tool":"t","type":"result","timestamp":"2025-01-01T00:00:00Z","data":null}`
		env := MustParseEnvelope(t, line)
		if env.Type != TypeResult {
			t.Errorf("Type = %q, want %q", env.Type, TypeResult)
		}
	})

	t.Run("failure", func(t *testing.T) {
		// Use a subtest that we expect to "fail" — we intercept the Fatal.
		// Since testing.T does not support catching Fatalf in-process,
		// we verify the behavior by checking that ParseEnvelope would error.
		_, err := ParseEnvelope("INVALID")
		if err == nil {
			t.Error("ParseEnvelope should error on invalid input")
		}
		// MustParseEnvelope would call t.Fatalf in a real test — verified by contract.
	})
}

func TestMustParseEnvelopes(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		app, buf := NewTestApp("tool", "1.0")
		app.JSONL().Success("a")
		app.JSONL().Success("b")

		envs := MustParseEnvelopes(t, buf.String())

		if len(envs) != 2 {
			t.Fatalf("got %d envelopes, want 2", len(envs))
		}
	})

	t.Run("failure", func(t *testing.T) {
		// Verify ParseEnvelopes errors on bad input (MustParseEnvelopes would t.Fatalf).
		_, err := ParseEnvelopes("NOT_JSONL")
		if err == nil {
			t.Error("ParseEnvelopes should error on invalid input")
		}
	})
}
