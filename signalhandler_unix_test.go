//go:build !windows

package agentsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestSignalHandlerWritesCrashDumpOnSIGINT(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("SIGINTTEST_HOME", tmpDir)
	defer os.Unsetenv("SIGINTTEST_HOME")

	sandbox := NewSandbox("sigint-test")
	fc := NewFlightContext()
	fc.Set("operation", "test-op")
	fc.Set("step", 3)

	var exitCalled int32
	stop := SetupSignalHandlerWithConfig("sigint-test", "1.0.0", "trace-sig-001", sandbox, fc, SignalHandlerConfig{
		OnSignal: func() { atomic.AddInt32(&exitCalled, 1) },
	})

	// Send SIGINT to ourselves.
	syscall.Kill(syscall.Getpid(), syscall.SIGINT)

	// Give the goroutine time to process.
	time.Sleep(200 * time.Millisecond)

	// Clean up.
	stop()

	if atomic.LoadInt32(&exitCalled) != 1 {
		t.Errorf("OnSignal called %d times, want 1", exitCalled)
	}

	// Verify crash dump was written.
	crashDir := sandbox.CrashDumpsDir()
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("ReadDir crash_dumps: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 crash dump file, got 0")
	}

	// Read and parse the crash dump.
	data, err := os.ReadFile(filepath.Join(crashDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var dump CrashDump
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if dump.CrashType != "signal" {
		t.Errorf("CrashType = %q, want %q", dump.CrashType, "signal")
	}
	if dump.Signal != "SIGINT" {
		t.Errorf("Signal = %q, want %q", dump.Signal, "SIGINT")
	}
	if dump.AppName != "sigint-test" {
		t.Errorf("AppName = %q, want %q", dump.AppName, "sigint-test")
	}
	if dump.AppVersion != "1.0.0" {
		t.Errorf("AppVersion = %q, want %q", dump.AppVersion, "1.0.0")
	}
	if dump.TraceID != "trace-sig-001" {
		t.Errorf("TraceID = %q, want %q", dump.TraceID, "trace-sig-001")
	}
	if dump.StackTrace == "" {
		t.Error("StackTrace should not be empty")
	}

	// Verify FlightContext snapshot.
	if dump.FlightContext == nil {
		t.Fatal("FlightContext is nil in crash dump")
	}
	if dump.FlightContext["operation"] != "test-op" {
		t.Errorf("FlightContext[operation] = %v, want %q", dump.FlightContext["operation"], "test-op")
	}
	if dump.FlightContext["step"] != float64(3) {
		t.Errorf("FlightContext[step] = %v, want 3", dump.FlightContext["step"])
	}
}

func TestSignalHandlerSIGTERM(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("SIGTERMTEST_HOME", tmpDir)
	defer os.Unsetenv("SIGTERMTEST_HOME")

	sandbox := NewSandbox("sigterm-test")
	fc := NewFlightContext()
	fc.Set("job_id", "abc-123")

	var exitCalled int32
	stop := SetupSignalHandlerWithConfig("sigterm-test", "2.0.0", "trace-term", sandbox, fc, SignalHandlerConfig{
		OnSignal: func() { atomic.AddInt32(&exitCalled, 1) },
	})

	// Send SIGTERM to ourselves.
	syscall.Kill(syscall.Getpid(), syscall.SIGTERM)

	time.Sleep(200 * time.Millisecond)
	stop()

	if atomic.LoadInt32(&exitCalled) != 1 {
		t.Errorf("OnSignal called %d times, want 1", exitCalled)
	}

	crashDir := sandbox.CrashDumpsDir()
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected crash dump file after SIGTERM")
	}

	data, _ := os.ReadFile(filepath.Join(crashDir, entries[len(entries)-1].Name()))
	var dump CrashDump
	if err := json.Unmarshal(data, &dump); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}

	if dump.CrashType != "signal" {
		t.Errorf("CrashType = %q, want %q", dump.CrashType, "signal")
	}
	if dump.Signal != "SIGTERM" {
		t.Errorf("Signal = %q, want %q", dump.Signal, "SIGTERM")
	}
	if dump.FlightContext["job_id"] != "abc-123" {
		t.Errorf("FlightContext[job_id] = %v, want %q", dump.FlightContext["job_id"], "abc-123")
	}
}

func TestSignalHandlerEmptyTraceID(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("SIGEMPTR_HOME", tmpDir)
	defer os.Unsetenv("SIGEMPTR_HOME")

	sandbox := NewSandbox("sig-emptr-test")
	fc := NewFlightContext()

	var exitCalled int32
	stop := SetupSignalHandlerWithConfig("sig-emptr-test", "1.0.0", "", sandbox, fc, SignalHandlerConfig{
		OnSignal: func() { atomic.AddInt32(&exitCalled, 1) },
	})

	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	time.Sleep(200 * time.Millisecond)
	stop()

	crashDir := sandbox.CrashDumpsDir()
	entries, _ := os.ReadDir(crashDir)
	if len(entries) == 0 {
		t.Fatal("expected crash dump file")
	}

	data, _ := os.ReadFile(filepath.Join(crashDir, entries[len(entries)-1].Name()))
	var dump CrashDump
	json.Unmarshal(data, &dump)

	if dump.TraceID != "" {
		t.Errorf("TraceID should be empty, got %q", dump.TraceID)
	}

	// With omitempty, empty trace_id should not appear in JSON.
	var raw map[string]interface{}
	json.Unmarshal(data, &raw)
	if _, exists := raw["trace_id"]; exists && raw["trace_id"] != "" {
		t.Errorf("trace_id should be omitted when empty, got: %v", raw["trace_id"])
	}
}

func TestSignalHandlerWriteError(t *testing.T) {
	// Create a sandbox whose crash_dumps dir is blocked by a file.
	tmpFile := filepath.Join(t.TempDir(), "blocker")
	os.WriteFile(tmpFile, []byte("block"), 0644)

	os.Setenv("SIGERR_HOME", tmpFile)
	defer os.Unsetenv("SIGERR_HOME")
	sandbox := NewSandbox("sig-err-test")

	fc := NewFlightContext()

	var writeErrors int32
	var exitCalled int32
	stop := SetupSignalHandlerWithConfig("sig-err-test", "1.0.0", "", sandbox, fc, SignalHandlerConfig{
		OnSignal: func() { atomic.AddInt32(&exitCalled, 1) },
		OnWriteError: func(err error) {
			atomic.AddInt32(&writeErrors, 1)
		},
	})

	syscall.Kill(syscall.Getpid(), syscall.SIGINT)
	time.Sleep(200 * time.Millisecond)
	stop()

	// The write should have failed, triggering the error callback.
	if atomic.LoadInt32(&writeErrors) != 1 {
		t.Errorf("expected 1 write error, got %d", writeErrors)
	}
	// OnSignal should still have been called even after write failure.
	if atomic.LoadInt32(&exitCalled) != 1 {
		t.Errorf("expected OnSignal to be called despite write error")
	}
}
