package agentsdk

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Sandbox manages the application's sandbox directory (~/.app-name/) and its
// standard subdirectories (data, cache, locks, crash_dumps).
//
// The base directory defaults to ~/.<app-name>/ and can be overridden by
// setting the environment variable <APP_NAME>_HOME (uppercased app name,
// hyphens replaced with underscores). For example, an app named "my-tool"
// would respect the MY_TOOL_HOME environment variable.
type Sandbox struct {
	appName  string
	baseDir  string
	subdirs  []string
}

// Standard subdirectory names.
const (
	SubdirData       = "data"
	SubdirCache      = "cache"
	SubdirLocks      = "locks"
	SubdirCrashDumps = "crash_dumps"
)

// NewSandbox creates a Sandbox for the given application name.
// The base directory is determined by:
//  1. The environment variable <APP_NAME>_HOME (uppercased, hyphens→underscores)
//  2. Falling back to ~/.<app-name>/ using os.UserHomeDir()
func NewSandbox(appName string) *Sandbox {
	subdirs := []string{SubdirData, SubdirCache, SubdirLocks, SubdirCrashDumps}

	envVar := strings.ToUpper(strings.ReplaceAll(appName, "-", "_")) + "_HOME"
	if dir := os.Getenv(envVar); dir != "" {
		return &Sandbox{
			appName: appName,
			baseDir: dir,
			subdirs: subdirs,
		}
	}

	homeDir, _ := os.UserHomeDir()
	baseDir := filepath.Join(homeDir, "."+appName)

	return &Sandbox{
		appName: appName,
		baseDir: baseDir,
		subdirs: subdirs,
	}
}

// Ensure idempotently creates the base directory and all subdirectories
// using os.MkdirAll with 0755 permissions.
func (s *Sandbox) Ensure() error {
	if err := os.MkdirAll(s.baseDir, 0755); err != nil {
		return fmt.Errorf("sandbox: create base dir %q: %w", s.baseDir, err)
	}
	for _, sub := range s.subdirs {
		path := filepath.Join(s.baseDir, sub)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("sandbox: create subdir %q: %w", path, err)
		}
	}
	return nil
}

// BaseDir returns the sandbox's base directory path.
func (s *Sandbox) BaseDir() string {
	return s.baseDir
}

// DataDir returns the data subdirectory path.
func (s *Sandbox) DataDir() string {
	return filepath.Join(s.baseDir, SubdirData)
}

// CacheDir returns the cache subdirectory path.
func (s *Sandbox) CacheDir() string {
	return filepath.Join(s.baseDir, SubdirCache)
}

// LocksDir returns the locks subdirectory path.
func (s *Sandbox) LocksDir() string {
	return filepath.Join(s.baseDir, SubdirLocks)
}

// CrashDumpsDir returns the crash_dumps subdirectory path.
func (s *Sandbox) CrashDumpsDir() string {
	return filepath.Join(s.baseDir, SubdirCrashDumps)
}

// Dirs returns a map of subdirectory names to their full paths.
// Useful for agent doctor checks and diagnostic tooling.
func (s *Sandbox) Dirs() map[string]string {
	return map[string]string{
		SubdirData:       s.DataDir(),
		SubdirCache:      s.CacheDir(),
		SubdirLocks:      s.LocksDir(),
		SubdirCrashDumps: s.CrashDumpsDir(),
	}
}
