package agentsdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// parseEnvelope is a test helper that parses a JSONL line into an Envelope.
func parseEnvelope(line string) (Envelope, error) {
	var env Envelope
	err := json.Unmarshal([]byte(line), &env)
	return env, err
}

// parseEnvelopes splits JSONL output into individual Envelope objects.
func parseEnvelopes(output string) ([]Envelope, error) {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	var envelopes []Envelope
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		env, err := parseEnvelope(line)
		if err != nil {
			return nil, fmt.Errorf("parse line %q: %w", line, err)
		}
		envelopes = append(envelopes, env)
	}
	return envelopes, nil
}

// newTestApp creates an App with a bytes.Buffer writer for testing.
func newTestApp(name, version string) (*App, *bytes.Buffer) {
	app := New(name, version)
	buf := &bytes.Buffer{}
	app.SetWriter(NewWriter(buf, name))
	return app, buf
}

// ---- Tests ----

func TestNewApp(t *testing.T) {
	app := New("test-tool", "1.0.0")
	if app.Name() != "test-tool" {
		t.Errorf("Name() = %q, want %q", app.Name(), "test-tool")
	}
	if app.Version() != "1.0.0" {
		t.Errorf("Version() = %q, want %q", app.Version(), "1.0.0")
	}
	if app.JSONL() == nil {
		t.Error("JSONL() returned nil")
	}
	if app.registry == nil {
		t.Error("registry is nil")
	}
}

func TestNewAppWithTraceID(t *testing.T) {
	os.Setenv("AGENT_TRACE_ID", "trace-123")
	defer os.Unsetenv("AGENT_TRACE_ID")

	app := New("test-tool", "1.0.0")
	buf := &bytes.Buffer{}
	w := NewWriter(buf, "test-tool")
	w.SetTraceID("trace-123") // preserve trace ID from env
	app.SetWriter(w)

	app.JSONL().Success(map[string]string{"ok": "true"})

	env, err := parseEnvelope(buf.String())
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.TraceID != "trace-123" {
		t.Errorf("TraceID = %q, want %q", env.TraceID, "trace-123")
	}
}

func TestNewAppWithoutTraceID(t *testing.T) {
	os.Unsetenv("AGENT_TRACE_ID")

	app := New("test-tool", "1.0.0")
	buf := &bytes.Buffer{}
	app.SetWriter(NewWriter(buf, "test-tool"))

	app.JSONL().Success(map[string]string{"ok": "true"})

	env, err := parseEnvelope(buf.String())
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.TraceID != "" {
		t.Errorf("TraceID = %q, want empty", env.TraceID)
	}
}

func TestAppJSONLWriter(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	err := app.JSONL().Success(map[string]string{"status": "ok"})
	if err != nil {
		t.Fatalf("Success: %v", err)
	}

	env, err := parseEnvelope(buf.String())
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	if err := ValidateEnvelope(env); err != nil {
		t.Errorf("ValidateEnvelope: %v", err)
	}
	if env.Type != TypeResult {
		t.Errorf("Type = %q, want %q", env.Type, TypeResult)
	}
}

func TestAppSetWriter(t *testing.T) {
	app := New("tool", "1.0")
	buf := &bytes.Buffer{}
	app.SetWriter(NewWriter(buf, "tool"))

	app.JSONL().Success("hello")

	if buf.Len() == 0 {
		t.Error("SetWriter did not redirect output to buffer")
	}
}

func TestExecuteSuccess(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Success(map[string]string{"done": "true"})
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1", len(envs))
	}
	if envs[0].Type != TypeResult {
		t.Errorf("Type = %q, want %q", envs[0].Type, TypeResult)
	}
}

func TestExecutePanicRecovery(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		panic("something went terribly wrong")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitFatalError {
		t.Errorf("Execute() = %d, want %d", code, ExitFatalError)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("expected JSONL output for panic, got empty")
	}

	env, err := parseEnvelope(strings.TrimSpace(output))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}

	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "FATAL_CRASH" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "FATAL_CRASH")
	}
	if !strings.Contains(env.Message, "panic: something went terribly wrong") {
		t.Errorf("Message = %q, should contain panic value", env.Message)
	}
	if !strings.Contains(env.Message, "Stack:") {
		t.Errorf("Message = %q, should contain stack trace excerpt", env.Message)
	}
}

func TestExecutePanicWithStackExcerpt(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		panic("stack-test")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitFatalError {
		t.Errorf("Execute() = %d, want %d", code, ExitFatalError)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	// The message should contain goroutine stack info
	if !strings.Contains(env.Message, "goroutine") {
		t.Errorf("Message should contain goroutine stack info, got: %q", env.Message)
	}
}

func TestExecuteExitError(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test"}
	rootCmd.SetArgs([]string{})
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("resource missing")}
	}

	code := app.Execute(rootCmd)

	if code != ExitNotFound {
		t.Errorf("Execute() = %d, want %d", code, ExitNotFound)
	}

	// No JSONL error output for ExitError (caller handles messaging)
	output := strings.TrimSpace(buf.String())
	if output != "" {
		t.Errorf("expected no JSONL output for ExitError, got: %q", output)
	}
}

func TestExecuteGenericError(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test"}
	rootCmd.SetArgs([]string{})
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("some generic error")
	}

	code := app.Execute(rootCmd)

	if code != ExitFatalError {
		t.Errorf("Execute() = %d, want %d", code, ExitFatalError)
	}

	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.Type != TypeError {
		t.Errorf("Type = %q, want %q", env.Type, TypeError)
	}
	if env.ErrorCode != "INTERNAL_ERROR" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "INTERNAL_ERROR")
	}
}

func TestExecuteQuietMode(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Progress(50, "halfway")
		app.JSONL().Warning("be careful")
		app.JSONL().Success(map[string]string{"done": "true"})
	}}
	rootCmd.SetArgs([]string{"--quiet"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}

	// Quiet mode should filter progress and warning, only success passes
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1 (progress and warning filtered)", len(envs))
	}
	if envs[0].Type != TypeResult {
		t.Errorf("Type = %q, want %q", envs[0].Type, TypeResult)
	}
}

func TestExecuteQuietFlagShort(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Warning("should be suppressed")
		app.JSONL().Success("ok")
	}}
	rootCmd.SetArgs([]string{"-q"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1 (warning filtered by -q)", len(envs))
	}
}

func TestExecuteQuietOffByDefault(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Progress(10, "starting")
		app.JSONL().Warning("watch out")
		app.JSONL().Success("ok")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 3 {
		t.Fatalf("got %d envelopes, want 3 (quiet off by default)", len(envs))
	}
}

func TestTraceIDInjection(t *testing.T) {
	os.Setenv("AGENT_TRACE_ID", "trace-abc-456")
	defer os.Unsetenv("AGENT_TRACE_ID")

	app := New("tool", "1.0")
	buf := &bytes.Buffer{}
	w := NewWriter(buf, "tool")
	w.SetTraceID("trace-abc-456") // preserve trace ID from env
	app.SetWriter(w)

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Success("step1")
		app.JSONL().Success("step2")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2", len(envs))
	}
	for i, env := range envs {
		if env.TraceID != "trace-abc-456" {
			t.Errorf("envelope[%d].TraceID = %q, want %q", i, env.TraceID, "trace-abc-456")
		}
	}
}

func TestRegisterErrorCode(t *testing.T) {
	app := New("tool", "1.0")

	err := app.RegisterErrorCode("CUSTOM_ERR", 42, "custom error for testing")
	if err != nil {
		t.Fatalf("RegisterErrorCode: %v", err)
	}

	exitCode := app.ErrorCodeToExitCode("CUSTOM_ERR")
	if exitCode != 42 {
		t.Errorf("ErrorCodeToExitCode(CUSTOM_ERR) = %d, want 42", exitCode)
	}
}

func TestRegisterBuiltinOverride(t *testing.T) {
	app := New("tool", "1.0")

	builtins := []string{"FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"}
	for _, code := range builtins {
		err := app.RegisterErrorCode(code, 99, "attempt override")
		if err == nil {
			t.Errorf("RegisterErrorCode(%q) should reject built-in override", code)
		}
	}
}

func TestErrorCodeToExitCodeUnknown(t *testing.T) {
	app := New("tool", "1.0")

	code := app.ErrorCodeToExitCode("NONEXISTENT_CODE")
	if code != ExitFatalError {
		t.Errorf("ErrorCodeToExitCode(unknown) = %d, want %d", code, ExitFatalError)
	}
}

// ---- Integration Test ----

func TestIntegrationFullFlow(t *testing.T) {
	// Set trace ID via env
	os.Setenv("AGENT_TRACE_ID", "int-trace-001")
	defer os.Unsetenv("AGENT_TRACE_ID")

	app := New("int-tool", "2.0.0")

	// Override writer with buffer for testing
	buf := &bytes.Buffer{}
	w := NewWriter(buf, "int-tool")
	w.SetTraceID("int-trace-001") // preserve trace ID from env
	app.SetWriter(w)

	// Register a custom error code
	err := app.RegisterErrorCode("DB_ERROR", ExitNetworkError, "database connection error")
	if err != nil {
		t.Fatalf("RegisterErrorCode: %v", err)
	}

	// Verify custom code is registered
	if code := app.ErrorCodeToExitCode("DB_ERROR"); code != ExitNetworkError {
		t.Errorf("ErrorCodeToExitCode(DB_ERROR) = %d, want %d", code, ExitNetworkError)
	}

	// Build a command tree
	rootCmd := &cobra.Command{Use: "int-tool"}
	subCmd := &cobra.Command{
		Use: "process",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Progress(25, "loading data")
			app.JSONL().Progress(50, "processing")
			app.JSONL().Warning("slow query detected")
			app.JSONL().Success(map[string]interface{}{
				"records": 42,
				"status":  "complete",
			})
			return nil
		},
	}
	rootCmd.AddCommand(subCmd)
	rootCmd.SetArgs([]string{"process"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 4 {
		t.Fatalf("got %d envelopes, want 4 (2 progress + 1 warning + 1 success)", len(envs))
	}

	// Verify envelope types in order
	expectedTypes := []string{TypeProgress, TypeProgress, TypeWarning, TypeResult}
	for i, env := range envs {
		if env.Type != expectedTypes[i] {
			t.Errorf("envelope[%d].Type = %q, want %q", i, env.Type, expectedTypes[i])
		}
		// All envelopes should have the trace ID
		if env.TraceID != "int-trace-001" {
			t.Errorf("envelope[%d].TraceID = %q, want %q", i, env.TraceID, "int-trace-001")
		}
		// Validate each envelope
		if err := ValidateEnvelope(env); err != nil {
			t.Errorf("envelope[%d] validation failed: %v", i, err)
		}
	}
}

func TestIntegrationQuietFullFlow(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "tool"}
	subCmd := &cobra.Command{
		Use: "run",
		Run: func(cmd *cobra.Command, args []string) {
			app.JSONL().Progress(10, "starting")
			app.JSONL().Progress(50, "halfway")
			app.JSONL().Warning("something odd")
			app.JSONL().ErrorWithCode("INPUT_INVALID", "bad input")
			app.JSONL().Success(map[string]string{"result": "done"})
		},
	}
	rootCmd.AddCommand(subCmd)
	rootCmd.SetArgs([]string{"--quiet", "run"})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}

	// Quiet filters progress+warning, error and result always pass
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2 (error + result, progress/warning filtered)", len(envs))
	}
	if envs[0].Type != TypeError {
		t.Errorf("envelope[0].Type = %q, want %q", envs[0].Type, TypeError)
	}
	if envs[1].Type != TypeResult {
		t.Errorf("envelope[1].Type = %q, want %q", envs[1].Type, TypeResult)
	}
}

func TestIntegrationPanicFlow(t *testing.T) {
	os.Setenv("AGENT_TRACE_ID", "panic-trace")
	defer os.Unsetenv("AGENT_TRACE_ID")

	app := New("panic-tool", "1.0")
	buf := &bytes.Buffer{}
	w := NewWriter(buf, "panic-tool")
	w.SetTraceID("panic-trace")
	app.SetWriter(w)

	rootCmd := &cobra.Command{Use: "panic-tool"}
	subCmd := &cobra.Command{
		Use: "explode",
		Run: func(cmd *cobra.Command, args []string) {
			app.JSONL().Progress(50, "about to blow")
			panic("kaboom!")
		},
	}
	rootCmd.AddCommand(subCmd)
	rootCmd.SetArgs([]string{"explode"})

	code := app.Execute(rootCmd)

	if code != ExitFatalError {
		t.Errorf("Execute() = %d, want %d", code, ExitFatalError)
	}

	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}

	// Expect 2 envelopes: progress (emitted before panic) + FATAL_CRASH error
	if len(envs) != 2 {
		t.Fatalf("got %d envelopes, want 2 (progress + FATAL_CRASH)", len(envs))
	}

	// First is progress
	if envs[0].Type != TypeProgress {
		t.Errorf("envelope[0].Type = %q, want %q", envs[0].Type, TypeProgress)
	}

	// Second is FATAL_CRASH
	fatalEnv := envs[1]
	if fatalEnv.Type != TypeError {
		t.Errorf("Type = %q, want %q", fatalEnv.Type, TypeError)
	}
	if fatalEnv.ErrorCode != "FATAL_CRASH" {
		t.Errorf("ErrorCode = %q, want %q", fatalEnv.ErrorCode, "FATAL_CRASH")
	}
	if !strings.Contains(fatalEnv.Message, "kaboom!") {
		t.Errorf("Message should contain panic value, got: %q", fatalEnv.Message)
	}
	if fatalEnv.TraceID != "panic-trace" {
		t.Errorf("TraceID = %q, want %q", fatalEnv.TraceID, "panic-trace")
	}
	if err := ValidateEnvelope(fatalEnv); err != nil {
		t.Errorf("ValidateEnvelope: %v", err)
	}
}

func TestIntegrationExitErrorFlow(t *testing.T) {
	app, buf := newTestApp("tool", "1.0")

	rootCmd := &cobra.Command{Use: "tool"}
	subCmd := &cobra.Command{
		Use: "find",
		RunE: func(cmd *cobra.Command, args []string) error {
			app.JSONL().Progress(30, "searching")
			return &ExitError{Code: ExitNotFound, Err: fmt.Errorf("item not found: %s", args[0])}
		},
	}
	rootCmd.AddCommand(subCmd)
	rootCmd.SetArgs([]string{"find", "missing-item"})

	code := app.Execute(rootCmd)

	if code != ExitNotFound {
		t.Errorf("Execute() = %d, want %d", code, ExitNotFound)
	}

	// Progress should have been emitted before the error
	envs, err := parseEnvelopes(buf.String())
	if err != nil {
		t.Fatalf("parse envelopes: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("got %d envelopes, want 1 (progress before ExitError)", len(envs))
	}
	if envs[0].Type != TypeProgress {
		t.Errorf("envelope[0].Type = %q, want %q", envs[0].Type, TypeProgress)
	}
}

// ---- Crash Dump Integration Tests ----

func TestAppFlightContextAccessor(t *testing.T) {
	app := New("fc-test", "1.0")
	if app.FlightContext() == nil {
		t.Fatal("FlightContext() returned nil")
	}

	// Should be usable for Set/Get.
	app.FlightContext().Set("key", "value")
	if app.FlightContext().Get("key") != "value" {
		t.Error("FlightContext Set/Get not working through accessor")
	}
}

func TestAppLogger(t *testing.T) {
	tmpDir := t.TempDir()
	app := New("logger-test", "1.0")
	app.sandbox = NewSandboxWithBaseDir("logger-test", tmpDir)

	// Recreate logger pointing to temp sandbox.
	logger, err := NewLogger(DefaultLoggerSettings("logger-test", app.Sandbox().LogsDir()))
	if err != nil {
		t.Fatalf("NewLogger: %v", err)
	}
	app.logger = logger

	// 1. Logger() returns non-nil.
	if app.Logger() == nil {
		t.Fatal("Logger() returned nil")
	}

	// 2. WithField + Info writes to sandbox logs dir.
	app.Logger().WithField("key", 123).Info("hello")
	app.Logger().Close()

	entries, err := os.ReadDir(app.Sandbox().LogsDir())
	if err != nil {
		t.Fatalf("ReadDir logs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected log file in sandbox logs dir, got 0")
	}

	data, err := os.ReadFile(filepath.Join(app.Sandbox().LogsDir(), entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile log: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "hello") {
		t.Errorf("log file should contain 'hello', got: %q", content)
	}
	if !strings.Contains(content, "key=123") {
		t.Errorf("log file should contain 'key=123', got: %q", content)
	}

	// 3. newTestApp properly resets logger — each call gets a fresh instance.
	app2, _ := newTestApp("logger-test-2", "1.0")
	if app2.Logger() == nil {
		t.Fatal("newTestApp Logger() returned nil")
	}
	if app2.Logger() == app.Logger() {
		t.Error("newTestApp should produce a fresh logger, not reuse previous")
	}
}

func TestExecutePanicWritesCrashDump(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("PANIC_DUMP_TEST_HOME", tmpDir)
	defer os.Unsetenv("PANIC_DUMP_TEST_HOME")

	app := New("panic-dump-test", "1.0.0")
	buf := &bytes.Buffer{}
	w := NewWriter(buf, "panic-dump-test")
	w.SetTraceID("panic-trace-001")
	app.SetWriter(w)

	// Override sandbox to use temp dir.
	app.sandbox = NewSandbox("panic-dump-test")

	// Store some flight context before panic.
	app.FlightContext().Set("current_step", "processing")
	app.FlightContext().Set("record_count", 42)

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		panic("catastrophic failure")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitFatalError {
		t.Errorf("Execute() = %d, want %d", code, ExitFatalError)
	}

	// Verify crash dump file was written.
	crashDir := app.Sandbox().CrashDumpsDir()
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("ReadDir crash_dumps: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected crash dump file after panic, got 0")
	}

	// Parse the crash dump.
	data, err := os.ReadFile(filepath.Join(crashDir, entries[len(entries)-1].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var dump CrashDump
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if dump.CrashType != "panic" {
		t.Errorf("CrashType = %q, want %q", dump.CrashType, "panic")
	}
	if dump.PanicValue != "catastrophic failure" {
		t.Errorf("PanicValue = %q, want %q", dump.PanicValue, "catastrophic failure")
	}
	if dump.AppName != "panic-dump-test" {
		t.Errorf("AppName = %q, want %q", dump.AppName, "panic-dump-test")
	}
	if dump.AppVersion != "1.0.0" {
		t.Errorf("AppVersion = %q, want %q", dump.AppVersion, "1.0.0")
	}
	if dump.TraceID != "panic-trace-001" {
		t.Errorf("TraceID = %q, want %q", dump.TraceID, "panic-trace-001")
	}
	if dump.StackTrace == "" {
		t.Error("StackTrace should not be empty")
	}
	if !containsSubstring(dump.StackTrace, "goroutine") {
		t.Error("StackTrace should contain goroutine info")
	}

	// Verify FlightContext values in crash dump.
	if dump.FlightContext == nil {
		t.Fatal("FlightContext is nil in crash dump")
	}
	if dump.FlightContext["current_step"] != "processing" {
		t.Errorf("FlightContext[current_step] = %v, want %q", dump.FlightContext["current_step"], "processing")
	}
	if dump.FlightContext["record_count"] != float64(42) {
		t.Errorf("FlightContext[record_count] = %v, want 42", dump.FlightContext["record_count"])
	}

	// Signal field should be omitted for panic-type (omitempty).
	if dump.Signal != "" {
		t.Errorf("Signal = %q, want empty for panic-type crash", dump.Signal)
	}

	// FATAL_CRASH JSONL should still be emitted (existing behavior preserved).
	env, err := parseEnvelope(strings.TrimSpace(buf.String()))
	if err != nil {
		t.Fatalf("parse envelope: %v", err)
	}
	if env.ErrorCode != "FATAL_CRASH" {
		t.Errorf("ErrorCode = %q, want %q", env.ErrorCode, "FATAL_CRASH")
	}
}

func TestExecuteNormalNoCrashDump(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("NORMAL_TEST_HOME", tmpDir)
	defer os.Unsetenv("NORMAL_TEST_HOME")

	app := New("normal-test", "1.0")
	app.sandbox = NewSandbox("normal-test")

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		app.JSONL().Success("all good")
	}}
	rootCmd.SetArgs([]string{})

	code := app.Execute(rootCmd)

	if code != ExitSuccess {
		t.Errorf("Execute() = %d, want %d", code, ExitSuccess)
	}

	// No crash dump should be written on normal exit.
	crashDir := app.Sandbox().CrashDumpsDir()
	entries, err := os.ReadDir(crashDir)
	if err == nil && len(entries) > 0 {
		t.Errorf("expected no crash dump files on normal exit, got %d", len(entries))
	}
}

func TestExecutePanicCrashDumpTimestamp(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("TSTAMP_TEST_HOME", tmpDir)
	defer os.Unsetenv("TSTAMP_TEST_HOME")

	app := New("tstamp-test", "1.0")
	app.sandbox = NewSandbox("tstamp-test")

	before := time.Now().UTC().Add(-time.Second)

	rootCmd := &cobra.Command{Use: "test", Run: func(cmd *cobra.Command, args []string) {
		panic("boom")
	}}
	rootCmd.SetArgs([]string{})

	app.Execute(rootCmd)

	after := time.Now().UTC().Add(time.Second)

	crashDir := app.Sandbox().CrashDumpsDir()
	entries, _ := os.ReadDir(crashDir)
	if len(entries) == 0 {
		t.Fatal("expected crash dump file")
	}

	data, _ := os.ReadFile(filepath.Join(crashDir, entries[0].Name()))
	var dump CrashDump
	json.Unmarshal(data, &dump)

	ts, err := time.Parse(time.RFC3339, dump.Timestamp)
	if err != nil {
		t.Fatalf("timestamp parse: %v", err)
	}

	ts = ts.UTC()
	if ts.Before(before) || ts.After(after) {
		t.Errorf("timestamp %v not between %v and %v", ts, before, after)
	}
}

func TestWriterTraceIDGetter(t *testing.T) {
	w := NewWriter(os.Stdout, "test")

	if w.TraceID() != "" {
		t.Error("TraceID() should be empty initially")
	}

	w.SetTraceID("abc-123")
	if w.TraceID() != "abc-123" {
		t.Errorf("TraceID() = %q, want %q", w.TraceID(), "abc-123")
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return strings.Contains(s, substr)
}
