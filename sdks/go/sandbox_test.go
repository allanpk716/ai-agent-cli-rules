package agentsdk

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

func TestSandboxDefaultBaseDir(t *testing.T) {
	// Clear any env override to test default behavior.
	os.Unsetenv("TEST_TOOL_HOME")

	s := NewSandbox("test-tool")

	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, ".test-tool")
	if s.BaseDir() != expected {
		t.Errorf("BaseDir() = %q, want %q", s.BaseDir(), expected)
	}
}

func TestSandboxEnvVarOverride(t *testing.T) {
	customDir := "/tmp/custom-test-tool-home"
	os.Setenv("TEST_TOOL_HOME", customDir)
	defer os.Unsetenv("TEST_TOOL_HOME")

	s := NewSandbox("test-tool")

	if s.BaseDir() != customDir {
		t.Errorf("BaseDir() = %q, want %q", s.BaseDir(), customDir)
	}
}

func TestSandboxEnvVarNameHyphens(t *testing.T) {
	// App name "my-cool-agent" should look for MY_COOL_AGENT_HOME
	customDir := "/tmp/my-cool-agent-home"
	os.Setenv("MY_COOL_AGENT_HOME", customDir)
	defer os.Unsetenv("MY_COOL_AGENT_HOME")

	s := NewSandbox("my-cool-agent")

	if s.BaseDir() != customDir {
		t.Errorf("BaseDir() = %q, want %q", s.BaseDir(), customDir)
	}
}

func TestSandboxSubdirPaths(t *testing.T) {
	os.Unsetenv("TEST_TOOL_HOME")

	s := NewSandbox("test-tool")

	expectedSubdirs := map[string]string{
		"data":        filepath.Join(s.BaseDir(), "data"),
		"cache":       filepath.Join(s.BaseDir(), "cache"),
		"locks":       filepath.Join(s.BaseDir(), "locks"),
		"crash_dumps": filepath.Join(s.BaseDir(), "crash_dumps"),
	}

	// Test individual accessors.
	if s.DataDir() != expectedSubdirs["data"] {
		t.Errorf("DataDir() = %q, want %q", s.DataDir(), expectedSubdirs["data"])
	}
	if s.CacheDir() != expectedSubdirs["cache"] {
		t.Errorf("CacheDir() = %q, want %q", s.CacheDir(), expectedSubdirs["cache"])
	}
	if s.LocksDir() != expectedSubdirs["locks"] {
		t.Errorf("LocksDir() = %q, want %q", s.LocksDir(), expectedSubdirs["locks"])
	}
	if s.CrashDumpsDir() != expectedSubdirs["crash_dumps"] {
		t.Errorf("CrashDumpsDir() = %q, want %q", s.CrashDumpsDir(), expectedSubdirs["crash_dumps"])
	}
}

func TestSandboxDirs(t *testing.T) {
	os.Unsetenv("TEST_TOOL_HOME")

	s := NewSandbox("test-tool")
	dirs := s.Dirs()

	expectedKeys := []string{"data", "cache", "locks", "crash_dumps"}
	var keys []string
	for k := range dirs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.Strings(expectedKeys)

	if len(keys) != len(expectedKeys) {
		t.Fatalf("Dirs() has %d keys, want %d", len(keys), len(expectedKeys))
	}
	for i, k := range keys {
		if k != expectedKeys[i] {
			t.Errorf("Dirs() key[%d] = %q, want %q", i, k, expectedKeys[i])
		}
	}

	// Each dir value should be baseDir + "/" + key.
	for name, path := range dirs {
		expected := filepath.Join(s.BaseDir(), name)
		if path != expected {
			t.Errorf("Dirs()[%q] = %q, want %q", name, path, expected)
		}
	}
}

func TestSandboxEnsureCreatesAllDirs(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("ENSURE_TEST_HOME", tmpDir)
	defer os.Unsetenv("ENSURE_TEST_HOME")

	s := NewSandbox("ensure-test")
	if err := s.Ensure(); err != nil {
		t.Fatalf("Ensure() failed: %v", err)
	}

	// Verify base dir exists.
	if info, err := os.Stat(s.BaseDir()); err != nil {
		t.Fatalf("base dir stat: %v", err)
	} else if !info.IsDir() {
		t.Fatalf("base dir is not a directory")
	}

	// Verify each subdir exists.
	for name, path := range s.Dirs() {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("subdir %q stat: %v", name, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("subdir %q is not a directory", name)
		}
	}
}

func TestSandboxEnsureIdempotent(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("IDEMP_TEST_HOME", tmpDir)
	defer os.Unsetenv("IDEMP_TEST_HOME")

	s := NewSandbox("idemp-test")

	// Call Ensure twice — both should succeed without error.
	if err := s.Ensure(); err != nil {
		t.Fatalf("first Ensure() failed: %v", err)
	}
	if err := s.Ensure(); err != nil {
		t.Fatalf("second Ensure() failed: %v", err)
	}

	// Directories should still exist.
	for name, path := range s.Dirs() {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("subdir %q missing after idempotent Ensure: %v", name, err)
		}
	}
}

func TestSandboxEnsureError(t *testing.T) {
	// Create a file where the base directory should be, causing MkdirAll to fail.
	tmpDir := t.TempDir()
	fileAsDir := filepath.Join(tmpDir, "blocking-file")
	if err := os.WriteFile(fileAsDir, []byte("block"), 0644); err != nil {
		t.Fatalf("setup: write blocking file: %v", err)
	}

	s := &Sandbox{
		appName: "error-test",
		baseDir: filepath.Join(fileAsDir, "subdir"),
		subdirs: []string{SubdirData, SubdirCache, SubdirLocks, SubdirCrashDumps},
	}

	err := s.Ensure()
	if err == nil {
		t.Fatal("Ensure() should fail when base dir path passes through a file, got nil")
	}

	// Error should contain the path for diagnostics.
	if !containsSubstr(err.Error(), s.baseDir) && !containsSubstr(err.Error(), fileAsDir) {
		t.Errorf("error should contain relevant path, got: %v", err)
	}
}

func TestSandboxAppIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	os.Setenv("INTEGRATION_TEST_HOME", tmpDir)
	defer os.Unsetenv("INTEGRATION_TEST_HOME")

	app := New("integration-test", "1.0.0")
	sb := app.Sandbox()

	if sb == nil {
		t.Fatal("App.Sandbox() returned nil")
	}

	if sb.BaseDir() != tmpDir {
		t.Errorf("Sandbox.BaseDir() = %q, want %q", sb.BaseDir(), tmpDir)
	}

	if err := sb.Ensure(); err != nil {
		t.Fatalf("Sandbox.Ensure() failed: %v", err)
	}

	// Verify sandbox dirs exist under the app's base.
	for name, path := range sb.Dirs() {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("subdir %q not created: %v", name, err)
		}
	}
}

// containsStr checks if s contains substr (test helper).
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
