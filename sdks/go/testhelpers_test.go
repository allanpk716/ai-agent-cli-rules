package agentsdk

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewTestAppBackwardCompat(t *testing.T) {
	app, buf := NewTestApp("compat-test", "1.0")

	if app.Name() != "compat-test" {
		t.Errorf("Name() = %q, want %q", app.Name(), "compat-test")
	}
	if app.Version() != "1.0" {
		t.Errorf("Version() = %q, want %q", app.Version(), "1.0")
	}
	if buf == nil {
		t.Fatal("buffer should not be nil")
	}

	// Sandbox should exist and use default home-based path.
	sb := app.Sandbox()
	if sb == nil {
		t.Fatal("Sandbox() should not be nil")
	}
	homeDir, _ := os.UserHomeDir()
	wantDir := filepath.Join(homeDir, ".compat-test")
	if sb.BaseDir() != wantDir {
		t.Errorf("BaseDir() = %q, want %q", sb.BaseDir(), wantDir)
	}
}

func TestNewTestAppWithTmpDir(t *testing.T) {
	tmp := t.TempDir()
	app, _ := NewTestApp("tmpdir-test", "1.0", WithTmpDir(tmp))

	sb := app.Sandbox()
	if sb == nil {
		t.Fatal("Sandbox() should not be nil")
	}
	if sb.BaseDir() != tmp {
		t.Errorf("BaseDir() = %q, want %q", sb.BaseDir(), tmp)
	}
}

func TestNewTestAppWithTmpDirSandboxOps(t *testing.T) {
	tmp := t.TempDir()
	app, _ := NewTestApp("ops-test", "1.0", WithTmpDir(tmp))

	sb := app.Sandbox()

	// Ensure should create subdirs under tmp.
	if err := sb.Ensure(); err != nil {
		t.Fatalf("Ensure() error: %v", err)
	}

	for _, sub := range []string{SubdirData, SubdirCache, SubdirLocks, SubdirCrashDumps} {
		path := filepath.Join(tmp, sub)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("subdir %q not created: %v", sub, err)
			continue
		}
		if !info.IsDir() {
			t.Errorf("%q exists but is not a directory", path)
		}
	}

	// Verify accessor methods return tmp-based paths.
	if sb.DataDir() != filepath.Join(tmp, SubdirData) {
		t.Errorf("DataDir() = %q, want %q", sb.DataDir(), filepath.Join(tmp, SubdirData))
	}
	if sb.CacheDir() != filepath.Join(tmp, SubdirCache) {
		t.Errorf("CacheDir() = %q, want %q", sb.CacheDir(), filepath.Join(tmp, SubdirCache))
	}
}
