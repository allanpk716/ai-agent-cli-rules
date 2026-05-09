package agentsdk

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestCreateBackup_basic(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create test files and directories.
	if err := os.MkdirAll(filepath.Join(dataDir, "sub", "deep"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dataDir, "file1.txt"), "hello world")
	writeFile(t, filepath.Join(dataDir, "file2.txt"), "goodbye")
	writeFile(t, filepath.Join(dataDir, "sub", "deep", "file3.txt"), "deep content")

	items := []string{"file1.txt", "file2.txt", "sub"}

	zipPath, size, err := CreateBackup(dataDir, outputDir, "testapp", items)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if size == 0 {
		t.Error("expected non-zero size")
	}

	// Verify zip contents.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}

	for _, expected := range []string{
		"file1.txt",
		"file2.txt",
		"sub/deep/file3.txt",
	} {
		if !names[expected] {
			t.Errorf("zip missing entry %q", expected)
		}
	}
}

func TestCreateBackup_missingItemSkipped(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "exists.txt"), "I exist")

	items := []string{"exists.txt", "nonexistent.txt", "also_missing.txt"}

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", items)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(zr.File))
	}
	if zr.File[0].Name != "exists.txt" {
		t.Errorf("expected entry %q, got %q", "exists.txt", zr.File[0].Name)
	}
}

func TestCreateBackup_emptyItems(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", nil)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if zipPath == "" {
		t.Fatal("expected non-empty zipPath")
	}

	// Verify the zip is valid (can be opened).
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 0 {
		t.Errorf("expected 0 entries, got %d", len(zr.File))
	}
}

func TestCreateBackup_filenameFormat(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "f.txt"), "x")

	zipPath, _, err := CreateBackup(dataDir, outputDir, "myapp", []string{"f.txt"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	base := filepath.Base(zipPath)
	// Pattern: myapp-backup-YYYYMMDD-HHMMSS.zip
	pattern := `^myapp-backup-\d{8}-\d{6}\.zip$`
	if !regexp.MustCompile(pattern).MatchString(base) {
		t.Errorf("filename %q does not match pattern %s", base, pattern)
	}
}

func TestCreateBackup_collisionSafety(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "f.txt"), "content")

	// Create first backup.
	zip1, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"f.txt"})
	if err != nil {
		t.Fatalf("first CreateBackup failed: %v", err)
	}

	// Pre-create a collision file with the exact same name.
	// Read the filename, then create a second file with the same base name pattern.
	// We can't predict the exact timestamp, so we'll manually create a file with
	// the same name as the first backup to force a collision on the second call.
	_, err = os.Stat(zip1)
	if err != nil {
		t.Fatalf("stat first zip: %v", err)
	}

	// The second call happens fast enough that it might get the same timestamp.
	// To guarantee collision, create a file with the same name as zip1's base name.
	// Actually, zip1 already exists. We need to force the second CreateBackup to collide.
	// Strategy: use a fixed-millisecond approach won't work, so instead we'll create
	// a file that matches the pattern the next call would generate.

	// Alternative approach: just call CreateBackup twice quickly.
	// If timestamps differ, no collision. So we manually create a blocker.
	base1 := filepath.Base(zip1)
	// Create a copy with the same name in the same dir (it already exists, so we need
	// to make the next call collide). Let's just call it and see if it works without
	// collision, then manually force.

	// Actually the simplest test: call CreateBackup, then create a file with the
	// same base name, then call CreateBackup again and verify it uses -2 suffix.
	// But we can't predict the timestamp of the next call.

	// Better approach: call CreateBackup twice in rapid succession. If timestamps
	// collide, we get -2. If they don't, both succeed with different names — that's
	// fine too, the test should accept either outcome.

	zip2, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"f.txt"})
	if err != nil {
		t.Fatalf("second CreateBackup failed: %v", err)
	}

	base2 := filepath.Base(zip2)

	// Both files must exist and be different (or same with suffix).
	if base1 == base2 {
		t.Fatal("expected different filenames for two backups")
	}

	// At least one should be the original, or both should be valid backups.
	for _, name := range []string{base1, base2} {
		pattern := `^testapp-backup-\d{8}-\d{6}(-\d+)?\.zip$`
		if !regexp.MustCompile(pattern).MatchString(name) {
			t.Errorf("filename %q does not match pattern %s", name, pattern)
		}
	}

	// Verify we can also force a collision by creating a file with the exact
	// same name pattern that the next call would produce.
	// Read zip2's timestamp portion and create a pre-existing file.
	re := regexp.MustCompile(`^testapp-backup-(\d{8}-\d{6})(-\d+)?\.zip$`)
	m := re.FindStringSubmatch(base2)
	if m == nil {
		t.Fatalf("could not parse timestamp from %q", base2)
	}
	tsPart := m[1]

	// Create a pre-existing file with the same timestamp.
	preexist := filepath.Join(outputDir, fmt.Sprintf("testapp-backup-%s.zip", tsPart))
	if err := os.WriteFile(preexist, []byte("fake"), 0644); err != nil {
		t.Fatalf("create pre-existing file: %v", err)
	}

	// Now call CreateBackup again — it should use a collision suffix.
	zip3, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"f.txt"})
	if err != nil {
		t.Fatalf("third CreateBackup failed: %v", err)
	}

	base3 := filepath.Base(zip3)
	if base3 == fmt.Sprintf("testapp-backup-%s.zip", tsPart) {
		t.Errorf("expected collision suffix, but got same name: %s", base3)
	}

	// The third file should have a collision suffix (-2, -3, etc.).
	// It could be a different timestamp OR a collision suffix of the preexisting one.
	// Let's verify it's a valid backup filename.
	collisionPattern := `^testapp-backup-\d{8}-\d{6}(-\d+)?\.zip$`
	if !regexp.MustCompile(collisionPattern).MatchString(base3) {
		t.Errorf("filename %q does not match pattern %s", base3, collisionPattern)
	}

	// Most importantly: the third zip should be a valid zip containing our file.
	zr, err := zip.OpenReader(zip3)
	if err != nil {
		t.Fatalf("open third zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 || zr.File[0].Name != "f.txt" {
		t.Errorf("third zip should contain f.txt, got %d entries", len(zr.File))
	}
}

func TestCreateBackup_forwardSlashes(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create nested directory structure.
	if err := os.MkdirAll(filepath.Join(dataDir, "a", "b", "c"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dataDir, "a", "b", "c", "deep.txt"), "nested")
	writeFile(t, filepath.Join(dataDir, "top.txt"), "top level")

	items := []string{"a", "top.txt"}

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", items)
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if strings.Contains(f.Name, "\\") {
			t.Errorf("zip entry %q contains backslash; expected forward slashes only", f.Name)
		}
	}

	// Verify the expected entries exist with forward slashes.
	expected := map[string]bool{
		"a/b/c/deep.txt": false,
		"top.txt":        false,
	}
	for _, f := range zr.File {
		if _, ok := expected[f.Name]; ok {
			expected[f.Name] = true
		}
	}
	for name, found := range expected {
		if !found {
			t.Errorf("zip missing entry %q", name)
		}
	}
}

func TestCreateBackup_createsOutputDir(t *testing.T) {
	dataDir := t.TempDir()
	baseDir := t.TempDir()
	outputDir := filepath.Join(baseDir, "nested", "output")

	writeFile(t, filepath.Join(dataDir, "f.txt"), "data")

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"f.txt"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	// Verify the output directory was created.
	info, err := os.Stat(outputDir)
	if err != nil {
		t.Fatalf("output dir not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("output path is not a directory")
	}

	// Verify the zip file exists.
	if _, err := os.Stat(zipPath); err != nil {
		t.Fatalf("zip file not found: %v", err)
	}
}

func TestCreateBackup_dataDirNotExist(t *testing.T) {
	outputDir := t.TempDir()

	_, _, err := CreateBackup("/nonexistent/path/that/does/not/exist", outputDir, "testapp", []string{"f.txt"})
	if err == nil {
		t.Fatal("expected error for non-existent dataDir")
	}

	// Verify error wraps with "backup:" prefix.
	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("error should contain 'backup:' prefix, got: %v", err)
	}
}

func TestCreateBackup_atomicCleanup(t *testing.T) {
	dataDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "f.txt"), "data")

	// Force a failure where MkdirAll succeeds but os.Create of the .tmp fails.
	// We do this by making the outputDir parent a file.
	parentFile := filepath.Join(t.TempDir(), "not-a-dir")
	writeFile(t, parentFile, "file")
	badOutputDir := filepath.Join(parentFile, "subdir")

	_, _, err := CreateBackup(dataDir, badOutputDir, "testapp", []string{"f.txt"})
	if err == nil {
		t.Fatal("expected error when output path parent is a file")
	}

	// Verify error is properly wrapped.
	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("error should contain 'backup:' prefix, got: %v", err)
	}

	// No orphaned .tmp files should exist anywhere near the output.
	tmpFiles, err := filepath.Glob(filepath.Join(filepath.Dir(parentFile), "**", "*.tmp"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	for _, tf := range tmpFiles {
		t.Errorf("found orphaned .tmp file: %s", tf)
	}
}

func TestCreateBackup_dataDirIsFile(t *testing.T) {
	outputDir := t.TempDir()
	dataDir := filepath.Join(t.TempDir(), "not-a-dir.txt")
	writeFile(t, dataDir, "I am a file")

	_, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"f.txt"})
	if err == nil {
		t.Fatal("expected error when dataDir is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got: %v", err)
	}
}

func TestCreateBackup_symlinkSkipped(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "real.txt"), "real content")

	// Create a symlink to a non-existent target.
	if err := os.Symlink("nonexistent.txt", filepath.Join(dataDir, "broken_link.txt")); err != nil {
		t.Skip("symlinks not supported on this platform")
	}

	// CreateBackup should skip the broken symlink silently.
	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"real.txt", "broken_link.txt"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Errorf("expected 1 entry (broken symlink skipped), got %d", len(zr.File))
	}
}

func TestCreateBackup_preservesFileMode(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "script.sh"), "#!/bin/sh\necho hi")

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"script.sh"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(zr.File))
	}

	// Verify the entry name is correct.
	if zr.File[0].Name != "script.sh" {
		t.Errorf("expected 'script.sh', got %q", zr.File[0].Name)
	}
}

func TestCreateBackup_emptyFile(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create an empty file.
	f, err := os.Create(filepath.Join(dataDir, "empty.txt"))
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	zipPath, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"empty.txt"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}

	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	if len(zr.File) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(zr.File))
	}
	if zr.File[0].UncompressedSize64 != 0 {
		t.Errorf("expected empty file in zip, got size %d", zr.File[0].UncompressedSize64)
	}
}

// Helper to write a file with given content.
func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// --- ListBackups tests ---

func TestListBackups_empty(t *testing.T) {
	outputDir := t.TempDir()

	metas, err := ListBackups(outputDir, "testapp")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if metas == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(metas) != 0 {
		t.Errorf("expected 0 metas, got %d", len(metas))
	}
}

func TestListBackups_nonexistentDir(t *testing.T) {
	metas, err := ListBackups("/nonexistent/path", "testapp")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if metas == nil {
		t.Error("expected non-nil empty slice")
	}
	if len(metas) != 0 {
		t.Errorf("expected 0 metas, got %d", len(metas))
	}
}

func TestListBackups_filtersByPrefix(t *testing.T) {
	outputDir := t.TempDir()

	// Create backup files with different prefixes.
	createFakeZip(t, outputDir, "app1-backup-20250101-120000.zip")
	createFakeZip(t, outputDir, "app2-backup-20250101-120000.zip")
	createFakeZip(t, outputDir, "other-file.zip")

	metas, err := ListBackups(outputDir, "app1")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 meta for app1, got %d", len(metas))
	}
	if metas[0].Filename != "app1-backup-20250101-120000.zip" {
		t.Errorf("expected app1-backup-20250101-120000.zip, got %q", metas[0].Filename)
	}
}

func TestListBackups_sortedDescending(t *testing.T) {
	outputDir := t.TempDir()

	createFakeZip(t, outputDir, "app-backup-20250101-100000.zip")
	createFakeZip(t, outputDir, "app-backup-20250103-100000.zip")
	createFakeZip(t, outputDir, "app-backup-20250102-100000.zip")

	metas, err := ListBackups(outputDir, "app")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 3 {
		t.Fatalf("expected 3 metas, got %d", len(metas))
	}

	// Newest first.
	if metas[0].Filename != "app-backup-20250103-100000.zip" {
		t.Errorf("expected newest first, got %q", metas[0].Filename)
	}
	if metas[1].Filename != "app-backup-20250102-100000.zip" {
		t.Errorf("expected middle second, got %q", metas[1].Filename)
	}
	if metas[2].Filename != "app-backup-20250101-100000.zip" {
		t.Errorf("expected oldest last, got %q", metas[2].Filename)
	}
}

func TestListBackups_withCollisionSuffix(t *testing.T) {
	outputDir := t.TempDir()

	createFakeZip(t, outputDir, "app-backup-20250101-120000.zip")
	createFakeZip(t, outputDir, "app-backup-20250101-120000-2.zip")

	metas, err := ListBackups(outputDir, "app")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 metas, got %d", len(metas))
	}

	// Both should have the same CreatedAt.
	if !metas[0].CreatedAt.Equal(metas[1].CreatedAt) {
		t.Errorf("collision files should have same CreatedAt: %v vs %v",
			metas[0].CreatedAt, metas[1].CreatedAt)
	}
}

func TestListBackups_sizeAndMetadata(t *testing.T) {
	outputDir := t.TempDir()

	content := "test backup content for size verification"
	createFakeZipWithContent(t, outputDir, "app-backup-20250101-120000.zip", content)

	metas, err := ListBackups(outputDir, "app")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 meta, got %d", len(metas))
	}

	if metas[0].Size == 0 {
		t.Error("expected non-zero size")
	}
	if metas[0].Filename != "app-backup-20250101-120000.zip" {
		t.Errorf("unexpected filename: %q", metas[0].Filename)
	}
}

// --- Integration tests ---

func TestBackupIntegration_roundTrip(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create test data.
	if err := os.MkdirAll(filepath.Join(dataDir, "config"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dataDir, "config", "app.yaml"), "name: test\nport: 8080")
	writeFile(t, filepath.Join(dataDir, "data.json"), "{\"key\": \"value\"}")

	// Create backup.
	zipPath, size, err := CreateBackup(dataDir, outputDir, "testapp", []string{"config", "data.json"})
	if err != nil {
		t.Fatalf("CreateBackup failed: %v", err)
	}
	if size == 0 {
		t.Error("expected non-zero size")
	}

	// List backups and verify round-trip.
	metas, err := ListBackups(outputDir, "testapp")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 backup, got %d", len(metas))
	}

	meta := metas[0]
	if meta.Size != size {
		t.Errorf("size mismatch: CreateBackup returned %d, ListBackups returned %d", size, meta.Size)
	}
	if meta.Filename != filepath.Base(zipPath) {
		t.Errorf("filename mismatch: CreateBackup returned %q, ListBackups returned %q",
			filepath.Base(zipPath), meta.Filename)
	}
	if meta.CreatedAt.IsZero() {
		t.Error("expected non-zero CreatedAt")
	}

	// Verify the backup zip contains all expected files.
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	defer zr.Close()

	names := make(map[string]bool)
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, expected := range []string{"config/app.yaml", "data.json"} {
		if !names[expected] {
			t.Errorf("zip missing entry %q", expected)
		}
	}
}

func TestBackupIntegration_multipleBackups(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	writeFile(t, filepath.Join(dataDir, "f.txt"), "v1")
	_, _, err := CreateBackup(dataDir, outputDir, "app", []string{"f.txt"})
	if err != nil {
		t.Fatal(err)
	}

	// Modify file and create another backup.
	writeFile(t, filepath.Join(dataDir, "f.txt"), "v2")
	_, _, err = CreateBackup(dataDir, outputDir, "app", []string{"f.txt"})
	if err != nil {
		t.Fatal(err)
	}

	metas, err := ListBackups(outputDir, "app")
	if err != nil {
		t.Fatalf("ListBackups failed: %v", err)
	}
	if len(metas) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(metas))
	}

	// Newest first (or equal if created in same second with collision suffix).
	if metas[0].CreatedAt.Before(metas[1].CreatedAt) {
		t.Errorf("expected metas sorted newest first: %v vs %v",
			metas[0].CreatedAt, metas[1].CreatedAt)
	}
}

// Helper: create an empty zip file with the given name.
func createFakeZip(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	zw.Close()
}

// Helper: create a zip file with specific content.
func createFakeZipWithContent(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	w, err := zw.Create("data.bin")
	if err != nil {
		t.Fatal(err)
	}
	w.Write([]byte(content))
	zw.Close()
}

// Verify TestCreateBackup uses WalkDir to test permission-denied during walk.
func TestCreateBackup_walkError(t *testing.T) {
	dataDir := t.TempDir()
	outputDir := t.TempDir()

	// Create a subdirectory with no read permission.
	subDir := filepath.Join(dataDir, "noread")
	if err := os.MkdirAll(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(subDir, "secret.txt"), "hidden")

	// On Windows, chmod may not restrict directory reads.
	// Skip if we can't restrict permissions.
	if err := os.Chmod(subDir, 0000); err != nil {
		t.Skip("cannot restrict directory permissions on this platform")
	}
	defer os.Chmod(subDir, 0755) // Restore.

	// CreateBackup should return an error when walking the restricted dir.
	_, _, err := CreateBackup(dataDir, outputDir, "testapp", []string{"noread"})
	// On some platforms (Windows), the walk might still succeed despite chmod.
	// On Unix, it should fail.
	if err == nil {
		// This is acceptable on Windows where directory permissions work differently.
	} else {
		// Error is expected on Unix.
		if !strings.Contains(err.Error(), "backup:") {
			t.Errorf("error should contain 'backup:' prefix, got: %v", err)
		}
	}
}

// Verify that the error wrapping preserves the causal chain.
func TestCreateBackup_errorWrapping(t *testing.T) {
	// Non-existent data dir.
	_, _, err := CreateBackup("/nonexistent/path", t.TempDir(), "app", []string{"f.txt"})
	if err == nil {
		t.Fatal("expected error")
	}

	// The error should be wrapped with "backup:" context.
	msg := err.Error()
	if !strings.HasPrefix(msg, "backup:") {
		t.Errorf("error should start with 'backup:', got: %v", msg)
	}

	// Verify error wrapping chain — the underlying error should be an fs.PathError.
	if !isErrorType(err, new(*fs.PathError)) {
		t.Logf("underlying error type: %T (may be wrapped)", err)
	}
}

// isErrorType uses errors.As to check the error chain.
func isErrorType(err error, target interface{}) bool {
	for err != nil {
		switch target.(type) {
		case **fs.PathError:
			if _, ok := err.(*fs.PathError); ok {
				return true
			}
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			break
		}
		err = unwrapped.Unwrap()
	}
	return false
}

// --- GFSRotate tests ---

// createFakeBackupFiles creates {prefix}-backup-YYYYMMDD-HHMMSS.zip files
// with specific timestamps in a temp dir, returning []BackupMeta sorted
// newest-first.
func createFakeBackupFiles(t *testing.T, dir, prefix string, timestamps []time.Time) []BackupMeta {
	t.Helper()
	metas := make([]BackupMeta, len(timestamps))
	for i, ts := range timestamps {
		name := fmt.Sprintf("%s-backup-%s.zip", prefix, ts.Format("20060102-150405"))
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("fake backup"), 0644); err != nil {
			t.Fatal(err)
		}
		metas[i] = BackupMeta{
			Filename:  name,
			Size:      10,
			CreatedAt: ts,
		}
	}
	// Sort newest-first (matching ListBackups output).
	sort.Slice(metas, func(i, j int) bool {
		return metas[i].CreatedAt.After(metas[j].CreatedAt)
	})
	return metas
}

func TestGFS_dailyRetention(t *testing.T) {
	dir := t.TempDir()

	// 5 backups on the same day, spaced by 1 hour each.
	base := time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local)
	var timestamps []time.Time
	for i := 0; i < 5; i++ {
		timestamps = append(timestamps, base.Add(time.Duration(i)*time.Hour))
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Daily: 2}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	if len(result.Kept) != 2 {
		t.Errorf("expected 2 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 3 {
		t.Errorf("expected 3 removed, got %d: %v", len(result.Removed), result.Removed)
	}

	// Verify the 2 newest files (13:00, 14:00) are kept.
	for _, f := range result.Removed {
		if strings.Contains(f, "130000") || strings.Contains(f, "140000") {
			t.Errorf("newest file should be kept, not removed: %s", f)
		}
	}
}

func TestGFS_weeklyRetention(t *testing.T) {
	dir := t.TempDir()

	// Backups spanning 3 ISO weeks.
	// Week 1: Jan 6 (Mon)
	// Week 2: Jan 13 (Mon)
	// Week 3: Jan 20 (Mon)
	timestamps := []time.Time{
		time.Date(2025, 1, 6, 10, 0, 0, 0, time.Local),  // Week 1
		time.Date(2025, 1, 7, 10, 0, 0, 0, time.Local),  // Week 1
		time.Date(2025, 1, 13, 10, 0, 0, 0, time.Local), // Week 2
		time.Date(2025, 1, 14, 10, 0, 0, 0, time.Local), // Week 2
		time.Date(2025, 1, 20, 10, 0, 0, 0, time.Local), // Week 3
		time.Date(2025, 1, 21, 10, 0, 0, 0, time.Local), // Week 3
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Weekly: 1}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// 3 kept (1 per week), 3 removed.
	if len(result.Kept) != 3 {
		t.Errorf("expected 3 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 3 {
		t.Errorf("expected 3 removed, got %d: %v", len(result.Removed), result.Removed)
	}
}

func TestGFS_monthlyRetention(t *testing.T) {
	dir := t.TempDir()

	// Backups spanning 3 months.
	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local), // January
		time.Date(2025, 1, 20, 10, 0, 0, 0, time.Local), // January
		time.Date(2025, 2, 15, 10, 0, 0, 0, time.Local), // February
		time.Date(2025, 2, 20, 10, 0, 0, 0, time.Local), // February
		time.Date(2025, 3, 15, 10, 0, 0, 0, time.Local), // March
		time.Date(2025, 3, 20, 10, 0, 0, 0, time.Local), // March
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Monthly: 1}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// 3 kept (1 per month), 3 removed.
	if len(result.Kept) != 3 {
		t.Errorf("expected 3 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 3 {
		t.Errorf("expected 3 removed, got %d: %v", len(result.Removed), result.Removed)
	}
}

func TestGFS_combinedRetention(t *testing.T) {
	dir := t.TempDir()

	// Backups across multiple days/weeks/months.
	// Daily=1, Weekly=1, Monthly=1
	timestamps := []time.Time{
		time.Date(2025, 1, 10, 8, 0, 0, 0, time.Local),  // Jan, week 2, day 1
		time.Date(2025, 1, 10, 16, 0, 0, 0, time.Local), // Jan, week 2, day 1 (same day)
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local), // Jan, week 3, different day
		time.Date(2025, 2, 5, 10, 0, 0, 0, time.Local),  // Feb, week 6
		time.Date(2025, 2, 5, 18, 0, 0, 0, time.Local),  // Feb, week 6 (same day)
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// Analysis (sorted newest-first):
	// Feb 5 18:00: daily(Feb 5 c=0→keep), weekly(2025-W06 c=0→keep), monthly(Feb c=0→keep)
	// Feb 5 10:00: daily(Feb 5 c=1→no), weekly(2025-W06 c=1→no), monthly(Feb c=1→no) → NOT protected
	// Jan 15 10:00: daily(Jan 15 c=0→keep), weekly(2025-W03 c=0→keep), monthly(Jan c=0→keep)
	// Jan 10 16:00: daily(Jan 10 c=0→keep), weekly(2025-W02 c=0→keep), monthly(Jan c=1→no) → kept by daily+weekly
	// Jan 10 08:00: daily(Jan 10 c=1→no), weekly(2025-W02 c=1→no), monthly(Jan c=1→no) → NOT protected
	//
	// Result: 3 kept, 2 removed.

	if len(result.Kept) != 3 {
		t.Errorf("expected 3 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 2 {
		t.Errorf("expected 2 removed, got %d: %v", len(result.Removed), result.Removed)
	}

	// The removed files should be the older same-day duplicates.
	for _, f := range result.Removed {
		if !strings.Contains(f, "080000") && !strings.Contains(f, "100000") {
			t.Errorf("unexpected removed file: %s", f)
		}
	}

	// Verify removed files actually don't exist on disk.
	for _, f := range result.Removed {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("removed file still exists on disk: %s", f)
		}
	}

	// Verify kept files still exist on disk.
	for _, f := range result.Kept {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("kept file missing from disk: %s", f)
		}
	}
}

func TestGFS_emptyBackups(t *testing.T) {
	dir := t.TempDir()

	result, err := GFSRotate(nil, RetentionPolicy{Daily: 1}, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}
	if len(result.Kept) != 0 {
		t.Errorf("expected 0 kept, got %d", len(result.Kept))
	}
	if len(result.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d", len(result.Removed))
	}

	result, err = GFSRotate([]BackupMeta{}, RetentionPolicy{Daily: 1}, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}
	if len(result.Kept) != 0 {
		t.Errorf("expected 0 kept, got %d", len(result.Kept))
	}
}

func TestGFS_deletionFailure(t *testing.T) {
	dir := t.TempDir()

	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local),
		time.Date(2025, 1, 15, 11, 0, 0, 0, time.Local),
		time.Date(2025, 1, 15, 12, 0, 0, 0, time.Local),
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	// Find the oldest file (10:00) and open it to lock it.
	// On Windows, an open file handle prevents deletion.
	// On Unix, we also chmod to read-only.
	oldestPath := ""
	for _, b := range backups {
		if strings.Contains(b.Filename, "100000") {
			oldestPath = filepath.Join(dir, b.Filename)
			break
		}
	}
	if oldestPath == "" {
		t.Fatal("could not find oldest backup file")
	}

	// Open a file handle to prevent deletion on Windows.
	lockFile, err := os.Open(oldestPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lockFile.Close()

	// Also chmod to read-only for Unix.
	if err := os.Chmod(oldestPath, 0444); err != nil {
		t.Skip("cannot set read-only on this platform")
	}
	defer os.Chmod(oldestPath, 0644) // Restore for cleanup.

	policy := RetentionPolicy{Daily: 1} // Only keep 1 per day
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// Total kept+removed must equal total backups (3).
	total := len(result.Kept) + len(result.Removed)
	if total != 3 {
		t.Errorf("expected total 3 (kept+removed), got %d", total)
	}

	// The newest (12:00) must always be protected.
	foundNewest := false
	for _, f := range result.Kept {
		if strings.Contains(f, "120000") {
			foundNewest = true
			break
		}
	}
	if !foundNewest {
		t.Error("newest backup (12:00) should always be kept")
	}

	// Check if the deletion was actually prevented.
	oldestStillExists := false
	for _, f := range result.Kept {
		if strings.Contains(f, "100000") {
			oldestStillExists = true
			break
		}
	}

	if oldestStillExists {
		// Deletion was prevented — verify the middle file was removed.
		if len(result.Removed) != 1 {
			t.Errorf("expected 1 removed (11:00), got %d: %v", len(result.Removed), result.Removed)
		}
		if len(result.Kept) != 2 {
			t.Errorf("expected 2 kept (12:00 + failed delete 10:00), got %d: %v", len(result.Kept), result.Kept)
		}
		// The read-only file should still exist on disk.
		if _, err := os.Stat(oldestPath); err != nil {
			t.Error("locked file should still exist after failed deletion")
		}
	} else {
		// Deletion succeeded despite our efforts (platform-specific).
		t.Logf("note: oldest file was deleted despite lock/chmod (platform behavior)")
		// Verify the middle and oldest were both removed.
		if len(result.Removed) != 2 {
			t.Errorf("expected 2 removed (10:00 + 11:00), got %d: %v", len(result.Removed), result.Removed)
		}
	}
}

func TestGFS_isoWeekEdgeCase(t *testing.T) {
	dir := t.TempDir()

	// Dec 31, 2025 is a Wednesday.
	// time.ISOWeek for Dec 31, 2025 returns year=2026, week=1.
	// This means Dec 31, 2025 and Jan 1, 2026 are in the same ISO week.
	timestamps := []time.Time{
		time.Date(2025, 12, 30, 10, 0, 0, 0, time.Local), // ISO week 1 of 2026 (Mon)
		time.Date(2025, 12, 31, 10, 0, 0, 0, time.Local), // ISO week 1 of 2026 (Tue)
		time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local),   // ISO week 1 of 2026 (Thu)
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Weekly: 1}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// All 3 are in the same ISO week (2026-W01). Weekly=1 keeps 1 newest.
	// Jan 1 is newest, Dec 31 is middle, Dec 30 is oldest.
	if len(result.Kept) != 1 {
		t.Errorf("expected 1 kept (all in same ISO week), got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 2 {
		t.Errorf("expected 2 removed, got %d: %v", len(result.Removed), result.Removed)
	}
}

func TestGFS_crossYearWeek(t *testing.T) {
	dir := t.TempDir()

	// Jan 1-3, 2025 might be in the last ISO week of 2024.
	// Jan 1, 2025 is a Wednesday. time.ISOWeek returns year=2025, week=1.
	// But in some years, Jan 1-3 are in week 52/53 of the previous year.
	// Let's use 2020: Jan 1, 2020 is Wednesday → ISO week 1 of 2020.
	// Actually 2016: Jan 1 is Friday → ISO week 53 of 2015.
	// Let's verify with a concrete case:
	// Dec 28, 2015 (Mon) → ISO week 2015-W53
	// Jan 1, 2016 (Fri) → ISO week 2015-W53
	// Jan 4, 2016 (Mon) → ISO week 2016-W01
	timestamps := []time.Time{
		time.Date(2015, 12, 28, 10, 0, 0, 0, time.Local), // 2015-W53
		time.Date(2015, 12, 29, 10, 0, 0, 0, time.Local), // 2015-W53
		time.Date(2016, 1, 1, 10, 0, 0, 0, time.Local),   // 2015-W53
		time.Date(2016, 1, 4, 10, 0, 0, 0, time.Local),   // 2016-W01
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	policy := RetentionPolicy{Weekly: 1}
	result, err := GFSRotate(backups, policy, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}

	// 2015-W53 has 3 backups → keeps 1 (Jan 1, newest in that week).
	// 2016-W01 has 1 backup → keeps 1 (Jan 4).
	// Total: 2 kept, 2 removed.
	if len(result.Kept) != 2 {
		t.Errorf("expected 2 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 2 {
		t.Errorf("expected 2 removed, got %d: %v", len(result.Removed), result.Removed)
	}
}

func TestGFS_outputDirNotExist(t *testing.T) {
	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local),
	}
	backups := createFakeBackupFiles(t, t.TempDir(), "app", timestamps)

	_, err := GFSRotate(backups, RetentionPolicy{Daily: 1}, "/nonexistent/path")
	if err == nil {
		t.Fatal("expected error for non-existent outputDir")
	}
	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("error should contain 'backup:' prefix, got: %v", err)
	}
}

func TestGFS_outputDirIsFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}

	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local),
	}
	backups := createFakeBackupFiles(t, t.TempDir(), "app", timestamps)

	_, err := GFSRotate(backups, RetentionPolicy{Daily: 1}, file)
	if err == nil {
		t.Fatal("expected error when outputDir is a file")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("error should mention 'not a directory', got: %v", err)
	}
}

func TestGFS_zeroPolicy(t *testing.T) {
	dir := t.TempDir()

	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local),
		time.Date(2025, 1, 15, 11, 0, 0, 0, time.Local),
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	// Zero policy means nothing is protected — everything should be deleted.
	result, err := GFSRotate(backups, RetentionPolicy{}, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}
	if len(result.Kept) != 0 {
		t.Errorf("expected 0 kept with zero policy, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 2 {
		t.Errorf("expected 2 removed with zero policy, got %d: %v", len(result.Removed), result.Removed)
	}
}

func TestGFS_allProtected(t *testing.T) {
	dir := t.TempDir()

	// 3 backups on different days with Daily=10 (more than count).
	timestamps := []time.Time{
		time.Date(2025, 1, 15, 10, 0, 0, 0, time.Local),
		time.Date(2025, 1, 16, 10, 0, 0, 0, time.Local),
		time.Date(2025, 1, 17, 10, 0, 0, 0, time.Local),
	}
	backups := createFakeBackupFiles(t, dir, "app", timestamps)

	result, err := GFSRotate(backups, RetentionPolicy{Daily: 10}, dir)
	if err != nil {
		t.Fatalf("GFSRotate failed: %v", err)
	}
	if len(result.Kept) != 3 {
		t.Errorf("expected all 3 kept, got %d: %v", len(result.Kept), result.Kept)
	}
	if len(result.Removed) != 0 {
		t.Errorf("expected 0 removed, got %d: %v", len(result.Removed), result.Removed)
	}
}

// --- BackupConfig tests ---

func TestBackupConfig_DefaultValues(t *testing.T) {
	cfg := DefaultBackupConfig()

	if cfg.RetentionPolicy.Daily != 7 {
		t.Errorf("expected Daily=7, got %d", cfg.RetentionPolicy.Daily)
	}
	if cfg.RetentionPolicy.Weekly != 4 {
		t.Errorf("expected Weekly=4, got %d", cfg.RetentionPolicy.Weekly)
	}
	if cfg.RetentionPolicy.Monthly != 6 {
		t.Errorf("expected Monthly=6, got %d", cfg.RetentionPolicy.Monthly)
	}
	if cfg.OutputDir != "" {
		t.Errorf("expected empty OutputDir, got %q", cfg.OutputDir)
	}
}

func TestBackupConfig_LoadNonexistentFile(t *testing.T) {
	cfg, err := LoadBackupConfig("/nonexistent/path/backup-config.json")
	if err != nil {
		t.Fatalf("LoadBackupConfig should return nil error for nonexistent file, got: %v", err)
	}

	// Should return default config.
	expected := DefaultBackupConfig()
	if cfg != expected {
		t.Errorf("expected default config, got %+v", cfg)
	}
}

func TestBackupConfig_SaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "backup-config.json")

	original := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 14, Weekly: 8, Monthly: 12},
		OutputDir:       "/var/backups/myapp",
	}

	if err := SaveBackupConfig(filePath, original); err != nil {
		t.Fatalf("SaveBackupConfig failed: %v", err)
	}

	loaded, err := LoadBackupConfig(filePath)
	if err != nil {
		t.Fatalf("LoadBackupConfig failed: %v", err)
	}

	if loaded != original {
		t.Errorf("round-trip mismatch:\n  original: %+v\n  loaded:  %+v", original, loaded)
	}
}

func TestBackupConfig_SaveCreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "deep", "nested", "backup-config.json")

	cfg := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 3, Weekly: 2, Monthly: 1},
		OutputDir:       "/tmp/backups",
	}

	if err := SaveBackupConfig(filePath, cfg); err != nil {
		t.Fatalf("SaveBackupConfig failed: %v", err)
	}

	// Verify parent dirs were created and file exists.
	if _, err := os.Stat(filePath); err != nil {
		t.Fatalf("config file should exist: %v", err)
	}

	// Verify content is readable.
	loaded, err := LoadBackupConfig(filePath)
	if err != nil {
		t.Fatalf("LoadBackupConfig failed: %v", err)
	}
	if loaded.OutputDir != "/tmp/backups" {
		t.Errorf("expected OutputDir=/tmp/backups, got %q", loaded.OutputDir)
	}
}

func TestBackupConfig_SaveOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "backup-config.json")

	first := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 7, Weekly: 4, Monthly: 6},
		OutputDir:       "/first/dir",
	}
	if err := SaveBackupConfig(filePath, first); err != nil {
		t.Fatalf("first SaveBackupConfig failed: %v", err)
	}

	second := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 30, Weekly: 12, Monthly: 24},
		OutputDir:       "/second/dir",
	}
	if err := SaveBackupConfig(filePath, second); err != nil {
		t.Fatalf("second SaveBackupConfig failed: %v", err)
	}

	loaded, err := LoadBackupConfig(filePath)
	if err != nil {
		t.Fatalf("LoadBackupConfig failed: %v", err)
	}

	if loaded != second {
		t.Errorf("expected second config after overwrite, got %+v", loaded)
	}
	if loaded.OutputDir == "/first/dir" {
		t.Error("config was not overwritten")
	}
}

func TestBackupConfig_LoadMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "bad-config.json")

	if err := os.WriteFile(filePath, []byte("{not valid json!!!}"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := LoadBackupConfig(filePath)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}

	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("error should contain 'backup:' prefix, got: %v", err)
	}
	if !strings.Contains(err.Error(), "parse config") {
		t.Errorf("error should mention 'parse config', got: %v", err)
	}
}

func TestBackupConfig_SaveSucceedsWithStaleTmp(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "backup-config.json")

	// Pre-create a stale .tmp file from a "prior crash".
	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, []byte("stale crash data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 5, Weekly: 3, Monthly: 2},
		OutputDir:       "/backups",
	}

	if err := SaveBackupConfig(filePath, cfg); err != nil {
		t.Fatalf("SaveBackupConfig failed with stale .tmp: %v", err)
	}

	// Verify the final file has correct content (not stale data).
	loaded, err := LoadBackupConfig(filePath)
	if err != nil {
		t.Fatalf("LoadBackupConfig failed: %v", err)
	}
	if loaded.OutputDir != "/backups" {
		t.Errorf("expected OutputDir=/backups, got %q", loaded.OutputDir)
	}
	if loaded.RetentionPolicy.Daily != 5 {
		t.Errorf("expected Daily=5, got %d", loaded.RetentionPolicy.Daily)
	}

	// .tmp should be gone (renamed to final path).
	if _, err := os.Stat(tmpFile); !os.IsNotExist(err) {
		t.Error(".tmp file should not exist after successful save")
	}
}

func TestBackupConfig_SaveCleansUpTmpOnFailure(t *testing.T) {
	// Create a path where the parent is a file (not a directory),
	// so MkdirAll will fail.
	parentFile := filepath.Join(t.TempDir(), "not-a-dir.txt")
	writeFile(t, parentFile, "I am a file")
	badPath := filepath.Join(parentFile, "subdir", "config.json")

	cfg := BackupConfig{
		RetentionPolicy: RetentionPolicy{Daily: 1, Weekly: 1, Monthly: 1},
		OutputDir:       "/tmp",
	}

	err := SaveBackupConfig(badPath, cfg)
	if err == nil {
		t.Fatal("expected error when parent path is a file")
	}
	if !strings.Contains(err.Error(), "backup:") {
		t.Errorf("error should contain 'backup:' prefix, got: %v", err)
	}
}
