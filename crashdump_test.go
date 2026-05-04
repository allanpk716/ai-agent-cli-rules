package agentsdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCrashDumpJSONStructure(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("agent", "planner")
	fc.Set("task_id", "T01")

	dump := &CrashDump{
		Timestamp:     time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC).Format(time.RFC3339),
		AppName:       "test-app",
		AppVersion:    "1.2.3",
		TraceID:       "trace-abc123",
		CrashType:     "signal",
		Signal:        "SIGTERM",
		StackTrace:    "goroutine 1 [running]:\nmain.main()",
		FlightContext: fc.Snapshot(),
	}

	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		t.Fatalf("json.MarshalIndent failed: %v", err)
	}

	// Parse back and verify all fields.
	var parsed map[string]interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	checks := map[string]string{
		"timestamp":  "2025-01-15T10:30:00Z",
		"app_name":   "test-app",
		"app_version": "1.2.3",
		"trace_id":   "trace-abc123",
		"crash_type": "signal",
		"signal":     "SIGTERM",
		"stack_trace": "goroutine 1 [running]:\nmain.main()",
	}
	for field, expected := range checks {
		v, ok := parsed[field]
		if !ok {
			t.Errorf("missing field %q in JSON output", field)
			continue
		}
		if s, _ := v.(string); s != expected {
			t.Errorf("field %q = %q, want %q", field, s, expected)
		}
	}

	// Verify flight_context nested object.
	fcRaw, ok := parsed["flight_context"]
	if !ok {
		t.Fatal("missing field flight_context")
	}
	fcMap, ok := fcRaw.(map[string]interface{})
	if !ok {
		t.Fatal("flight_context is not a JSON object")
	}
	if fcMap["agent"] != "planner" {
		t.Errorf("flight_context.agent = %v, want %q", fcMap["agent"], "planner")
	}
}

func TestCrashDumpPanicTypeOmitsSignal(t *testing.T) {
	dump := &CrashDump{
		Timestamp:   time.Now().Format(time.RFC3339),
		AppName:     "test-app",
		AppVersion:  "1.0.0",
		CrashType:   "panic",
		PanicValue:  "runtime error: index out of range",
		StackTrace:  "goroutine 1 [running]:\nmain.main()",
		FlightContext: map[string]interface{}{},
	}

	data, err := json.Marshal(dump)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	s := string(data)
	// signal should be omitted (omitempty) for panic-type crashes.
	if strings.Contains(s, `"signal"`) {
		t.Errorf("panic-type crash dump should omit signal field, got: %s", s)
	}
	// panic_value should be present.
	if !strings.Contains(s, `"panic_value"`) {
		t.Error("panic-type crash dump should contain panic_value field")
	}
}

func TestWriteCrashDumpCreatesFile(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("CRASHTEST_HOME", tmpDir)
	defer os.Unsetenv("CRASHTEST_HOME")

	sandbox := NewSandbox("crashtest")
	fc := NewFlightContext()
	fc.Set("step", "processing")

	dump := &CrashDump{
		Timestamp:     time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
		AppName:       "crashtest",
		AppVersion:    "0.1.0",
		TraceID:       "trace-001",
		CrashType:     "signal",
		Signal:        "SIGINT",
		StackTrace:    "fake stack",
		FlightContext: fc.Snapshot(),
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	// Verify a file was created in crash_dumps/.
	crashDir := sandbox.CrashDumpsDir()
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) failed: %v", crashDir, err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 file in crash_dumps/, got %d", len(entries))
	}

	filename := entries[0].Name()
	if !strings.HasPrefix(filename, "crash-") {
		t.Errorf("filename %q should start with 'crash-'", filename)
	}
	if !strings.HasSuffix(filename, ".json") {
		t.Errorf("filename %q should end with '.json'", filename)
	}

	// Verify contents can be parsed back.
	data, err := os.ReadFile(filepath.Join(crashDir, filename))
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}

	var parsed CrashDump
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if parsed.Signal != "SIGINT" {
		t.Errorf("parsed.Signal = %q, want %q", parsed.Signal, "SIGINT")
	}
	if parsed.FlightContext["step"] != "processing" {
		t.Errorf("parsed.FlightContext[step] = %v, want %q", parsed.FlightContext["step"], "processing")
	}
}

func TestWriteCrashDumpAtomicWrite(t *testing.T) {
	// Verify no .tmp file is left behind after a successful write.
	tmpDir := t.TempDir()
	os.Setenv("ATOMICTEST_HOME", tmpDir)
	defer os.Unsetenv("ATOMICTEST_HOME")

	sandbox := NewSandbox("atomic-test")

	dump := &CrashDump{
		Timestamp:     time.Now().Format(time.RFC3339),
		AppName:       "atomic-test",
		AppVersion:    "1.0.0",
		CrashType:     "panic",
		PanicValue:    "test panic",
		StackTrace:    "stack",
		FlightContext: map[string]interface{}{},
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	crashDir := sandbox.CrashDumpsDir()
	entries, _ := os.ReadDir(crashDir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover .tmp file: %q", e.Name())
		}
	}
}

func TestWriteCrashDumpCreatesDirectory(t *testing.T) {
	// Write to a sandbox where the crash_dumps/ directory doesn't exist yet.
	tmpDir := t.TempDir()
	os.Setenv("MKDIRTEST_HOME", tmpDir)
	defer os.Unsetenv("MKDIRTEST_HOME")

	sandbox := NewSandbox("mkdir-test")

	// Verify the directory doesn't exist yet.
	crashDir := sandbox.CrashDumpsDir()
	if _, err := os.Stat(crashDir); !os.IsNotExist(err) {
		t.Fatalf("crash_dumps/ should not exist before write, got: %v", err)
	}

	dump := &CrashDump{
		Timestamp:     time.Now().Format(time.RFC3339),
		AppName:       "mkdir-test",
		AppVersion:    "1.0.0",
		CrashType:     "signal",
		Signal:        "SIGTERM",
		StackTrace:    "stack trace here",
		FlightContext: map[string]interface{}{},
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump should create missing dir, got: %v", err)
	}

	// Directory should now exist with a file in it.
	entries, err := os.ReadDir(crashDir)
	if err != nil {
		t.Fatalf("ReadDir after write failed: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 file after write, got %d", len(entries))
	}
}

func TestWriteCrashDumpEmptyFlightContext(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("EMPTYFC_HOME", tmpDir)
	defer os.Unsetenv("EMPTYFC_HOME")

	sandbox := NewSandbox("empty-fc-test")

	dump := &CrashDump{
		Timestamp:     time.Now().Format(time.RFC3339),
		AppName:       "empty-fc-test",
		AppVersion:    "1.0.0",
		CrashType:     "panic",
		PanicValue:    "nil pointer",
		StackTrace:    "stack",
		FlightContext: map[string]interface{}{}, // empty but non-nil
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump with empty FlightContext failed: %v", err)
	}

	crashDir := sandbox.CrashDumpsDir()
	entries, _ := os.ReadDir(crashDir)
	if len(entries) != 1 {
		t.Fatalf("expected 1 file, got %d", len(entries))
	}

	data, _ := os.ReadFile(filepath.Join(crashDir, entries[0].Name()))
	var parsed CrashDump
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(parsed.FlightContext) != 0 {
		t.Errorf("FlightContext should be empty, got %d items", len(parsed.FlightContext))
	}
}

func TestWriteCrashDumpLargeContextValues(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("LARGECTX_HOME", tmpDir)
	defer os.Unsetenv("LARGECTX_HOME")

	sandbox := NewSandbox("large-ctx-test")
	fc := NewFlightContext()

	// Store large values in the FlightContext.
	largeSlice := make([]string, 1000)
	for i := range largeSlice {
		largeSlice[i] = strings.Repeat("x", 100)
	}
	fc.Set("large_data", largeSlice)
	fc.Set("nested", map[string]interface{}{
		"level1": map[string]interface{}{
			"level2": "deep value",
		},
	})

	dump := &CrashDump{
		Timestamp:     time.Now().Format(time.RFC3339),
		AppName:       "large-ctx-test",
		AppVersion:    "1.0.0",
		CrashType:     "signal",
		Signal:        "SIGTERM",
		StackTrace:    "stack",
		FlightContext: fc.Snapshot(),
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump with large context failed: %v", err)
	}

	crashDir := sandbox.CrashDumpsDir()
	entries, _ := os.ReadDir(crashDir)
	data, _ := os.ReadFile(filepath.Join(crashDir, entries[0].Name()))

	var parsed CrashDump
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	largeArr, ok := parsed.FlightContext["large_data"].([]interface{})
	if !ok {
		t.Fatal("large_data should be a JSON array")
	}
	if len(largeArr) != 1000 {
		t.Errorf("large_data length = %d, want 1000", len(largeArr))
	}

	nested, ok := parsed.FlightContext["nested"].(map[string]interface{})
	if !ok {
		t.Fatal("nested should be a JSON object")
	}
	level1, ok := nested["level1"].(map[string]interface{})
	if !ok {
		t.Fatal("nested.level1 should be a JSON object")
	}
	if level1["level2"] != "deep value" {
		t.Errorf("nested.level1.level2 = %v, want %q", level1["level2"], "deep value")
	}
}

func TestWriteCrashDumpFilenameFormat(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("FNNAMETEST_HOME", tmpDir)
	defer os.Unsetenv("FNNAMETEST_HOME")

	sandbox := NewSandbox("fnname-test")

	dump := &CrashDump{
		Timestamp:     time.Now().Format(time.RFC3339),
		AppName:       "fnname-test",
		AppVersion:    "1.0.0",
		CrashType:     "signal",
		Signal:        "SIGTERM",
		StackTrace:    "stack",
		FlightContext: map[string]interface{}{},
	}

	if err := WriteCrashDump(sandbox, dump); err != nil {
		t.Fatalf("WriteCrashDump failed: %v", err)
	}

	entries, _ := os.ReadDir(sandbox.CrashDumpsDir())
	filename := entries[0].Name()

	// Filename should match crash-YYYYMMDD-HHMMSS.json
	if len(filename) != len("crash-20060102-150405.json") {
		t.Errorf("filename %q has unexpected length %d", filename, len(filename))
	}
	if !strings.HasPrefix(filename, "crash-") || !strings.HasSuffix(filename, ".json") {
		t.Errorf("filename %q should match crash-YYYYMMDD-HHMMSS.json", filename)
	}
}
