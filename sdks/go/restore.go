package agentsdk

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// RestoreResult holds the outcome of a RestoreBackup call.
type RestoreResult struct {
	Restored []string // Paths (relative to targetDir) of successfully restored files.
	Skipped  []string // Paths of zip entries that were skipped (e.g. not in items filter).
}

// RestoreBackup extracts files from a zip archive created by CreateBackup into
// targetDir.
//
// Parameters:
//   - zipPath: path to the zip archive.
//   - targetDir: directory to extract files into (created if it does not exist).
//   - items: optional list of entry names to restore. When nil or empty, all
//     entries are restored. When non-empty, only entries whose name exactly
//     matches an item or has an item as a directory prefix are restored.
//
// On success, returns RestoreResult with restored file paths.
// On the first error during extraction, it aborts and returns the error together
// with any files already restored.
//
// Security: zip-slip path traversal is detected and rejected.
// Integrity: archive/zip performs CRC-32 verification during decompression.
func RestoreBackup(zipPath, targetDir string, items []string) (RestoreResult, error) {
	// Open the zip archive.
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore: open zip %q: %w", zipPath, err)
	}
	defer r.Close()

	// Ensure targetDir exists.
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return RestoreResult{}, fmt.Errorf("restore: create target dir %q: %w", targetDir, err)
	}

	// Build item lookup set for selective restore.
	itemSet := make(map[string]bool, len(items))
	for _, it := range items {
		itemSet[it] = true
	}
	selective := len(itemSet) > 0

	// Normalise targetDir to an absolute, clean path for zip-slip checks.
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return RestoreResult{}, fmt.Errorf("restore: resolve target dir %q: %w", targetDir, err)
	}

	result := RestoreResult{
		Restored: make([]string, 0),
		Skipped:  make([]string, 0),
	}

	for _, f := range r.File {
		// Directory entries — create them but don't count as restored files.
		if f.FileInfo().IsDir() {
			if selective && !matchesItem(f.Name, itemSet) {
				result.Skipped = append(result.Skipped, f.Name)
				continue
			}
			dirPath := filepath.Join(absTarget, filepath.FromSlash(f.Name))
			if isZipSlip(f.Name, absTarget) {
				return result, fmt.Errorf("restore: zip-slip detected for directory entry %q", f.Name)
			}
			if err := os.MkdirAll(dirPath, 0755); err != nil {
				return result, fmt.Errorf("restore: create directory %q: %w", dirPath, err)
			}
			continue
		}

		// Selective restore filter: skip non-matching files.
		if selective && !matchesItem(f.Name, itemSet) {
			result.Skipped = append(result.Skipped, f.Name)
			continue
		}

		// Zip-slip check.
		if isZipSlip(f.Name, absTarget) {
			return result, fmt.Errorf("restore: zip-slip detected for entry %q", f.Name)
		}

		destPath := filepath.Join(absTarget, filepath.FromSlash(f.Name))

		if err := restoreFile(f, destPath); err != nil {
			return result, fmt.Errorf("restore: extract %q: %w", f.Name, err)
		}

		// Record relative path using forward slashes (matching zip entry convention).
		result.Restored = append(result.Restored, f.Name)
	}

	return result, nil
}

// matchesItem checks whether a zip entry name should be restored when
// selective item filtering is active.
//
// An entry matches if:
//   - Its name exactly equals an item (e.g. "config.json" == "config.json").
//   - An item is a directory prefix of the entry (e.g. item "data/" matches
//     entry "data/file.txt").
func matchesItem(entryName string, itemSet map[string]bool) bool {
	if itemSet[entryName] {
		return true
	}
	// Check directory-prefix match: item "data" should match "data/file.txt".
	// Normalise both to use forward slashes.
	for item := range itemSet {
		prefix := strings.TrimSuffix(item, "/") + "/"
		if strings.HasPrefix(entryName, prefix) {
			return true
		}
	}
	return false
}

// isZipSlip returns true if entryPath would resolve to a location outside
// targetDir after joining, indicating a path-traversal (zip-slip) attack.
func isZipSlip(entryPath, targetDir string) bool {
	// Clean the entry path and join with target dir.
	cleaned := filepath.Join(targetDir, filepath.FromSlash(entryPath))
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return true // treat resolution failure as suspicious
	}
	// Ensure the resolved path starts with the target dir.
	absTarget, err := filepath.Abs(targetDir)
	if err != nil {
		return true
	}
	return !strings.HasPrefix(abs+string(filepath.Separator), absTarget+string(filepath.Separator))
}

// restoreFile extracts a single zip file entry to destPath, creating parent
// directories as needed. Reading the file content triggers archive/zip's
// built-in CRC-32 integrity verification.
func restoreFile(zf *zip.File, destPath string) error {
	// Create parent directories.
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("create parent dirs: %w", err)
	}

	// Open the compressed file data (this validates CRC-32 on read).
	rc, err := zf.Open()
	if err != nil {
		return fmt.Errorf("open entry: %w", err)
	}
	defer rc.Close()

	// Create (or overwrite) the destination file.
	out, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, rc); err != nil {
		return fmt.Errorf("write file: %w", err)
	}

	return nil
}
