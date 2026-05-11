package agentsdk

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
)

// logLineRegex matches the expected log format:
//
//	YYYY-MM-DD HH:MM:SS.mmm - [LEVEL]: message key=value
var logLineRegex = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} - \[\w+\]:`)

// levelRegex extracts the level from a log line.
var levelRegex = regexp.MustCompile(`\[(DEBUG|INFO|WARNING|ERROR|FATAL|PANIC)\]`)

// findLogFile returns the path of the first non-directory file in dir.
func findLogFile(t *testing.T, dir string) string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir %q: %v", dir, err)
	}

	for _, e := range entries {
		if !e.IsDir() {
			return filepath.Join(dir, e.Name())
		}
	}
	t.Fatalf("no files found in %q", dir)
	return ""
}

// readLogLines reads all non-empty lines from the first .log file found in dir.
func readLogLines(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read log dir %q: %v", dir, err)
	}

	var logPath string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			logPath = filepath.Join(dir, e.Name())
			break
		}
	}
	// Also check files without .log extension from rotatelogs patterns
	if logPath == "" {
		for _, e := range entries {
			if !e.IsDir() {
				logPath = filepath.Join(dir, e.Name())
				break
			}
		}
	}
	if logPath == "" {
		t.Fatalf("no log files found in %q", dir)
	}

	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("failed to read log file %q: %v", logPath, err)
	}

	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// createTestLogger creates a Logger writing to t.TempDir() with sensible test defaults.
// It returns the Logger and the log directory path.
func createTestLogger(t *testing.T) (*Logger, string) {
	t.Helper()
	dir := t.TempDir()
	settings := DefaultLoggerSettings("testapp", dir)
	settings.RotationTime = 24 * time.Hour
	settings.MaxAgeDays = 1

	logger, err := NewLogger(settings)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	t.Cleanup(func() { logger.Close() })
	return logger, dir
}

// --- Settings Tests ---

func TestNewLoggerDefaultSettings(t *testing.T) {
	settings := DefaultLoggerSettings("myapp", "/tmp/logs")

	if settings.Level != logrus.InfoLevel {
		t.Errorf("expected Level=InfoLevel, got %v", settings.Level)
	}
	if settings.LogDir != "/tmp/logs" {
		t.Errorf("expected LogDir=/tmp/logs, got %q", settings.LogDir)
	}
	if settings.LogNameBase != "myapp" {
		t.Errorf("expected LogNameBase=myapp, got %q", settings.LogNameBase)
	}
	if settings.RotationTime != 24*time.Hour {
		t.Errorf("expected RotationTime=24h, got %v", settings.RotationTime)
	}
	if settings.MaxAgeDays != 7 {
		t.Errorf("expected MaxAgeDays=7, got %d", settings.MaxAgeDays)
	}
	if settings.MaxSizeMB != 0 {
		t.Errorf("expected MaxSizeMB=0, got %d", settings.MaxSizeMB)
	}
	if settings.UseHierarchicalPath != false {
		t.Errorf("expected UseHierarchicalPath=false, got %v", settings.UseHierarchicalPath)
	}
}

// --- Format Tests ---

func TestLoggerInfoFormat(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.Info("hello world")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("expected at least one log line, got none")
	}

	line := lines[len(lines)-1] // last line is most recent
	if !logLineRegex.MatchString(line) {
		t.Errorf("log line does not match expected format\n  got: %q\n  regex: %s", line, logLineRegex.String())
	}

	// Verify specific format components.
	if !strings.Contains(line, "[INFO]") {
		t.Errorf("log line missing [INFO]: %q", line)
	}
	if !strings.Contains(line, "hello world") {
		t.Errorf("log line missing message: %q", line)
	}
}

// --- Level Tests ---

func TestLoggerAllLevels(t *testing.T) {
	tests := []struct {
		name      string
		level     logrus.Level
		logFunc   func(*Logger)
		wantLevel string
	}{
		{"Debug", logrus.DebugLevel, func(l *Logger) { l.Debug("dbg msg") }, "DEBUG"},
		{"Info", logrus.InfoLevel, func(l *Logger) { l.Info("inf msg") }, "INFO"},
		{"Warn", logrus.WarnLevel, func(l *Logger) { l.Warn("wrn msg") }, "WARNING"},
		{"Error", logrus.ErrorLevel, func(l *Logger) { l.Error("err msg") }, "ERROR"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			settings := DefaultLoggerSettings("leveltest", dir)
			settings.Level = tc.level
			settings.RotationTime = 24 * time.Hour
			settings.MaxAgeDays = 1

			logger, err := NewLogger(settings)
			if err != nil {
				t.Fatalf("NewLogger failed: %v", err)
			}
			defer logger.Close()

			tc.logFunc(logger)

			lines := readLogLines(t, dir)
			if len(lines) == 0 {
				t.Fatal("expected at least one log line")
			}

			line := lines[len(lines)-1]
			matches := levelRegex.FindStringSubmatch(line)
			if len(matches) < 2 {
				t.Fatalf("no level found in log line: %q", line)
			}
			if matches[1] != tc.wantLevel {
				t.Errorf("expected level %q, got %q in line: %q", tc.wantLevel, matches[1], line)
			}
		})
	}
}

// --- WithField / WithFields Tests ---

func TestLoggerWithField(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.WithField("user_id", 123).Info("login")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("expected at least one log line")
	}

	line := lines[len(lines)-1]
	if !strings.Contains(line, "user_id=123") {
		t.Errorf("log line missing user_id=123: %q", line)
	}
	if !strings.Contains(line, "login") {
		t.Errorf("log line missing message: %q", line)
	}
}

func TestLoggerWithFields(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.WithFields(map[string]interface{}{"a": 1, "b": "two"}).Info("test")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("expected at least one log line")
	}

	line := lines[len(lines)-1]
	if !strings.Contains(line, "a=1") {
		t.Errorf("log line missing a=1: %q", line)
	}
	if !strings.Contains(line, "b=two") {
		t.Errorf("log line missing b=two: %q", line)
	}
	if !strings.Contains(line, "test") {
		t.Errorf("log line missing message: %q", line)
	}
}

// --- Degradation Tests ---

func TestLoggerDegradationToStderr(t *testing.T) {
	// Use a file path as the "directory" — MkdirAll on a file path will fail.
	tmpFile := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(tmpFile, []byte("block"), 0644); err != nil {
		t.Fatalf("failed to create blocking file: %v", err)
	}

	settings := DefaultLoggerSettings("degtest", tmpFile)
	logger, err := NewLogger(settings)
	if err != nil {
		t.Fatalf("NewLogger should not return error on unwritable dir, got: %v", err)
	}
	defer logger.Close()

	// Should not panic.
	logger.Info("this goes to stderr only")
}

// --- Close Tests ---

func TestLoggerClose(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.Info("before close")

	if err := logger.Close(); err != nil {
		t.Errorf("Close returned error: %v", err)
	}

	// Verify log file was written.
	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Error("expected log lines before close")
	}
}

func TestLoggerCloseIdempotent(t *testing.T) {
	logger, _ := createTestLogger(t)

	if err := logger.Close(); err != nil {
		t.Errorf("first Close returned error: %v", err)
	}
	if err := logger.Close(); err != nil {
		t.Errorf("second Close returned error: %v", err)
	}
}

// --- Independence Tests ---

func TestLoggerMultipleInstances(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()

	settings1 := DefaultLoggerSettings("app1", dir1)
	settings1.RotationTime = 24 * time.Hour
	settings1.MaxAgeDays = 1
	logger1, err := NewLogger(settings1)
	if err != nil {
		t.Fatalf("NewLogger(app1) failed: %v", err)
	}
	defer logger1.Close()

	settings2 := DefaultLoggerSettings("app2", dir2)
	settings2.RotationTime = 24 * time.Hour
	settings2.MaxAgeDays = 1
	logger2, err := NewLogger(settings2)
	if err != nil {
		t.Fatalf("NewLogger(app2) failed: %v", err)
	}
	defer logger2.Close()

	logger1.Info("from_app1")
	logger2.Info("from_app2")

	lines1 := readLogLines(t, dir1)
	lines2 := readLogLines(t, dir2)

	if len(lines1) == 0 {
		t.Fatal("logger1 produced no output")
	}
	if len(lines2) == 0 {
		t.Fatal("logger2 produced no output")
	}

	// Verify isolation.
	for _, line := range lines1 {
		if strings.Contains(line, "from_app2") {
			t.Errorf("logger1 output contains logger2 message: %q", line)
		}
	}
	for _, line := range lines2 {
		if strings.Contains(line, "from_app1") {
			t.Errorf("logger2 output contains logger1 message: %q", line)
		}
	}
}

// --- Rotation Tests ---

func TestLoggerFileRotation(t *testing.T) {
	dir := t.TempDir()
	settings := DefaultLoggerSettings("rottest", dir)
	settings.RotationTime = 1 * time.Hour
	settings.MaxAgeDays = 1

	logger, err := NewLogger(settings)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	logger.Info("rotation test")

	// Verify at least one file was created in the log directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if !e.IsDir() {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one log file in directory")
	}
}

// --- Negative Tests ---

func TestLoggerEmptyMessage(t *testing.T) {
	logger, dir := createTestLogger(t)

	// Empty string message — should not panic.
	logger.Info("")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("expected log line even for empty message")
	}

	line := lines[len(lines)-1]
	if !logLineRegex.MatchString(line) {
		t.Errorf("empty-message log line does not match format: %q", line)
	}
}

func TestLoggerNilFieldsMap(t *testing.T) {
	logger, dir := createTestLogger(t)

	// nil map to WithFields — should not panic.
	logger.WithFields(nil).Info("nil fields")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("expected log line with nil fields")
	}

	line := lines[len(lines)-1]
	if !strings.Contains(line, "nil fields") {
		t.Errorf("missing message: %q", line)
	}
}

func TestLoggerSpecialCharacters(t *testing.T) {
	logger, dir := createTestLogger(t)

	msg := "special: \t\n\"quotes\" and 'apostrophes' & <tags>"
	logger.Info(msg)

	// The message contains newlines, so the log file will have multiple lines.
	// Read the full file content instead of individual lines.
	data, err := os.ReadFile(findLogFile(t, dir))
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "special:") {
		t.Errorf("missing message content in: %q", content)
	}
	if !strings.Contains(content, "quotes") {
		t.Errorf("missing quotes in: %q", content)
	}
	if !strings.Contains(content, "apostrophes") {
		t.Errorf("missing apostrophes in: %q", content)
	}
}

func TestLoggerSizeBasedRotation(t *testing.T) {
	dir := t.TempDir()
	settings := DefaultLoggerSettings("sizetest", dir)
	settings.MaxSizeMB = 1 // enable size-based rotation
	settings.MaxAgeDays = 1

	logger, err := NewLogger(settings)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}
	defer logger.Close()

	logger.Info("size rotation test")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read log dir: %v", err)
	}

	found := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".log") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected .log file with size-based rotation")
	}
}

// --- Close on stderr-only Logger ---

func TestLoggerCloseStderrOnly(t *testing.T) {
	// Create a logger that degraded to stderr-only.
	tmpFile := filepath.Join(t.TempDir(), "blocker")
	if err := os.WriteFile(tmpFile, []byte("x"), 0644); err != nil {
		t.Fatalf("failed to create blocker: %v", err)
	}

	settings := DefaultLoggerSettings("stdtest", tmpFile)
	logger, err := NewLogger(settings)
	if err != nil {
		t.Fatalf("NewLogger failed: %v", err)
	}

	// Close on stderr-only logger should be a safe no-op.
	if err := logger.Close(); err != nil {
		t.Errorf("Close on stderr-only logger returned error: %v", err)
	}
}

// --- WithField string value with spaces ---

func TestLoggerWithFieldComplexValues(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.WithField("path", "/some/path with spaces/file.txt").Info("complex")
	logger.WithField("count", 42).WithField("ratio", 3.14).Info("multi")

	lines := readLogLines(t, dir)
	if len(lines) < 2 {
		t.Fatal("expected at least 2 log lines")
	}

	line1 := lines[len(lines)-2]
	if !strings.Contains(line1, "path=/some/path") {
		t.Errorf("missing path field: %q", line1)
	}

	line2 := lines[len(lines)-1]
	if !strings.Contains(line2, "count=42") {
		t.Errorf("missing count field: %q", line2)
	}
	if !strings.Contains(line2, "ratio=3.14") {
		t.Errorf("missing ratio field: %q", line2)
	}
}

// --- Test helper for format verification ---

func TestLoggerFormatContract(t *testing.T) {
	logger, dir := createTestLogger(t)

	logger.WithField("key", 123).Info("hello")

	lines := readLogLines(t, dir)
	if len(lines) == 0 {
		t.Fatal("no log lines")
	}

	line := lines[len(lines)-1]

	// Full contract: time - [LEVEL]: msg key=value
	// Timestamp: YYYY-MM-DD HH:MM:SS.mmm
	tsPattern := `\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}`
	fullPattern := fmt.Sprintf(`^%s - \[INFO\]: hello key=123$`, tsPattern)
	matched, err := regexp.MatchString(fullPattern, line)
	if err != nil {
		t.Fatalf("regex error: %v", err)
	}
	if !matched {
		t.Errorf("log line does not match full format contract\n  got:      %q\n  expected: %s", line, fullPattern)
	}
}
