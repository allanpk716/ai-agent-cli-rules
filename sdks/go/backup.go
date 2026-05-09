package agentsdk

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// BackupMeta holds metadata about a backup archive.
type BackupMeta struct {
	Filename  string    // Base filename, e.g. "myapp-backup-20250101-120000.zip"
	Size      int64     // File size in bytes
	CreatedAt time.Time // Parsed from filename timestamp
}

// CreateBackup generates a collision-safe, timestamped zip archive of the
// specified items relative to dataDir and writes it to outputDir.
//
// Filename format: {prefix}-backup-YYYYMMDD-HHMMSS.zip
// On collision, appends incrementing suffix: -2, -3, ...
//
// Zip entry paths use forward slashes for cross-platform compatibility.
// Non-existent items in the items slice are silently skipped.
// The zip is written atomically via a .tmp file + os.Rename.
func CreateBackup(dataDir, outputDir, prefix string, items []string) (zipPath string, size int64, err error) {
	// Validate dataDir exists.
	info, err := os.Stat(dataDir)
	if err != nil {
		return "", 0, fmt.Errorf("backup: stat data dir %q: %w", dataDir, err)
	}
	if !info.IsDir() {
		return "", 0, fmt.Errorf("backup: data dir %q is not a directory", dataDir)
	}

	// Create output directory if needed.
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", 0, fmt.Errorf("backup: create output dir %q: %w", outputDir, err)
	}

	// Generate base filename from current timestamp.
	now := time.Now()
	baseName := fmt.Sprintf("%s-backup-%s.zip", prefix, now.Format("20060102-150405"))

	// Resolve collision by appending -2, -3, ...
	target := filepath.Join(outputDir, baseName)
	finalTarget := target
	for i := 2; ; i++ {
		if _, err := os.Stat(finalTarget); os.IsNotExist(err) {
			break
		}
		if err != nil {
			return "", 0, fmt.Errorf("backup: check existing file %q: %w", finalTarget, err)
		}
		finalTarget = filepath.Join(outputDir, fmt.Sprintf("%s-backup-%s-%d.zip", prefix, now.Format("20060102-150405"), i))
	}

	// Atomic write: create .tmp file, then rename.
	tmpPath := finalTarget + ".tmp"

	var writeErr error
	func() {
		f, err := os.Create(tmpPath)
		if err != nil {
			writeErr = fmt.Errorf("backup: create temp file %q: %w", tmpPath, err)
			return
		}
		defer func() {
			if cerr := f.Close(); cerr != nil && writeErr == nil {
				writeErr = fmt.Errorf("backup: close temp file %q: %w", tmpPath, cerr)
			}
		}()

		zw := zip.NewWriter(f)
		defer func() {
			if cerr := zw.Close(); cerr != nil && writeErr == nil {
				writeErr = fmt.Errorf("backup: close zip writer: %w", cerr)
			}
		}()

		for _, item := range items {
			srcPath := filepath.Join(dataDir, item)
			itemInfo, err := os.Stat(srcPath)
			if err != nil {
				// Silently skip non-existent or inaccessible items.
				continue
			}

			if itemInfo.IsDir() {
				// Walk the directory tree.
				err = filepath.Walk(srcPath, func(walkPath string, walkInfo os.FileInfo, walkErr error) error {
					if walkErr != nil {
						return walkErr
					}
					if walkInfo.IsDir() {
						return nil
					}

					rel, err := filepath.Rel(dataDir, walkPath)
					if err != nil {
						return fmt.Errorf("backup: compute relative path for %q: %w", walkPath, err)
					}
					// Force forward slashes in zip entry names.
					zipName := filepath.ToSlash(rel)

					if err := addFileToZip(zw, walkPath, zipName); err != nil {
						return fmt.Errorf("backup: add file %q: %w", walkPath, err)
					}
					return nil
				})
				if err != nil {
					writeErr = fmt.Errorf("backup: walk directory %q: %w", srcPath, err)
					return
				}
			} else {
				// Single file.
				zipName := filepath.ToSlash(item)
				if err := addFileToZip(zw, srcPath, zipName); err != nil {
					writeErr = fmt.Errorf("backup: add file %q: %w", srcPath, err)
					return
				}
			}
		}
	}()

	if writeErr != nil {
		// Clean up temp file on failure.
		os.Remove(tmpPath)
		return "", 0, writeErr
	}

	// Atomic rename from .tmp to final target.
	if err := os.Rename(tmpPath, finalTarget); err != nil {
		os.Remove(tmpPath)
		return "", 0, fmt.Errorf("backup: rename %q to %q: %w", tmpPath, finalTarget, err)
	}

	// Read back size.
	finalInfo, err := os.Stat(finalTarget)
	if err != nil {
		return finalTarget, 0, fmt.Errorf("backup: stat output file %q: %w", finalTarget, err)
	}

	return finalTarget, finalInfo.Size(), nil
}

// addFileToZip writes a single file to a zip writer.
func addFileToZip(zw *zip.Writer, filePath, zipName string) error {
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open %q: %w", filePath, err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("stat %q: %w", filePath, err)
	}

	hdr, err := zip.FileInfoHeader(info)
	if err != nil {
		return fmt.Errorf("file info header for %q: %w", filePath, err)
	}
	hdr.Name = zipName
	// Use zero compression method for deterministic sizes (optional, but simplifies testing).
	// Standard deflate is fine for production; use stored for test stability.
	hdr.Method = zip.Deflate

	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return fmt.Errorf("create zip entry %q: %w", zipName, err)
	}

	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("write zip entry %q: %w", zipName, err)
	}

	return nil
}

// ListBackups scans outputDir for files matching {prefix}-backup-*.zip and
// returns their metadata sorted by CreatedAt descending (newest first).
// Returns an empty (non-nil) slice if no backups are found or the directory
// does not exist.
func ListBackups(outputDir, prefix string) ([]BackupMeta, error) {
	metas := make([]BackupMeta, 0) // Always non-nil empty slice.

	pattern := prefix + "-backup-*.zip"
	matches, err := filepath.Glob(filepath.Join(outputDir, pattern))
	if err != nil {
		return nil, fmt.Errorf("backup: glob %q: %w", pattern, err)
	}

	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			// Skip files we can't stat.
			continue
		}

		meta := BackupMeta{
			Filename: filepath.Base(m),
			Size:     info.Size(),
		}

		// Parse timestamp from filename.
		// Expected: {prefix}-backup-YYYYMMDD-HHMMSS.zip or {prefix}-backup-YYYYMMDD-HHMMSS-N.zip
		meta.CreatedAt, err = parseBackupFilename(meta.Filename, prefix)
		if err != nil {
			// If we can't parse the timestamp, use zero time.
			meta.CreatedAt = time.Time{}
		}

		metas = append(metas, meta)
	}

	// Sort by CreatedAt descending (newest first).
	// Files with zero time go to the end.
	for i := 0; i < len(metas)-1; i++ {
		for j := i + 1; j < len(metas); j++ {
			if metas[j].CreatedAt.After(metas[i].CreatedAt) {
				metas[i], metas[j] = metas[j], metas[i]
			}
		}
	}

	return metas, nil
}

// parseBackupFilename extracts the timestamp from a backup filename.
// Formats: {prefix}-backup-YYYYMMDD-HHMMSS.zip or {prefix}-backup-YYYYMMDD-HHMMSS-N.zip
func parseBackupFilename(filename, prefix string) (time.Time, error) {
	expectedPrefix := prefix + "-backup-"
	if !strings.HasPrefix(filename, expectedPrefix) {
		return time.Time{}, fmt.Errorf("backup: filename %q does not have prefix %q", filename, expectedPrefix)
	}

	rest := filename[len(expectedPrefix):]
	// Remove .zip extension.
	rest = strings.TrimSuffix(rest, ".zip")

	// Check for collision suffix: YYYYMMDD-HHMMSS-N
	tsPart := rest
	// "YYYYMMDD-HHMMSS" is 15 chars, dash at index 8 is part of timestamp.
	// Any dash at index 15 or later indicates a collision suffix (-N).
	lastDash := strings.LastIndex(rest, "-")
	if lastDash >= 15 {
		suffixStr := rest[lastDash+1:]
		if _, err := fmt.Sscanf(suffixStr, "%d", new(int)); err == nil {
			tsPart = rest[:lastDash]
		}
	}

	// Parse "YYYYMMDD-HHMMSS"
	return time.ParseInLocation("20060102-150405", tsPart, time.Local)
}

// RetentionPolicy defines how many backups to retain per time granularity.
type RetentionPolicy struct {
	Daily   int `json:"daily"`   // Number of newest backups to keep per calendar day.
	Weekly  int `json:"weekly"`  // Number of newest backups to keep per ISO week.
	Monthly int `json:"monthly"` // Number of newest backups to keep per calendar month.
}

// RotationResult reports which backups were kept and which were removed by GFSRotate.
type RotationResult struct {
	Kept    []string // Filenames of protected (kept) backups.
	Removed []string // Filenames of successfully deleted backups.
}

// GFSRotate performs grandfather-father-son rotation on the given backups.
//
// Backups are already expected to be sorted newest-first (as returned by
// ListBackups). For each retention rule (daily, weekly, monthly), the N newest
// backups within each group (calendar day, ISO week, calendar month) are marked
// as protected. A backup protected by ANY rule is kept.
//
// Unprotected backups are deleted from outputDir. Individual deletion failures
// are logged but do not halt rotation — the failed file remains in the Kept list.
//
// Returns an error only if outputDir does not exist or is not a directory.
func GFSRotate(backups []BackupMeta, policy RetentionPolicy, outputDir string) (RotationResult, error) {
	// Validate outputDir.
	info, err := os.Stat(outputDir)
	if err != nil {
		return RotationResult{}, fmt.Errorf("backup: stat output dir %q: %w", outputDir, err)
	}
	if !info.IsDir() {
		return RotationResult{}, fmt.Errorf("backup: output dir %q is not a directory", outputDir)
	}

	result := RotationResult{
		Kept:    make([]string, 0),
		Removed: make([]string, 0),
	}

	if len(backups) == 0 {
		return result, nil
	}

	// Build protected set: filename → true.
	protected := make(map[string]bool)

	// Group keys and counters.
	type groupKey struct {
		rule  string // "daily", "weekly", "monthly"
		group string // YYYYMMDD, YYYY-WNN, or YYYYMM
	}
	groupCounts := make(map[groupKey]int)

	for _, b := range backups {
		isoYear, week := b.CreatedAt.ISOWeek()
		month := b.CreatedAt.Format("200601")
		dayKey := b.CreatedAt.Format("20060102")
		weekKey := fmt.Sprintf("%04d-W%02d", isoYear, week)

		// Check each rule; a backup is protected if ANY rule protects it.
		dailyKey := groupKey{"daily", dayKey}
		weeklyKey := groupKey{"weekly", weekKey}
		monthlyKey := groupKey{"monthly", month}

		if policy.Daily > 0 && groupCounts[dailyKey] < policy.Daily {
			protected[b.Filename] = true
			groupCounts[dailyKey]++
		}
		if policy.Weekly > 0 && groupCounts[weeklyKey] < policy.Weekly {
			protected[b.Filename] = true
			groupCounts[weeklyKey]++
		}
		if policy.Monthly > 0 && groupCounts[monthlyKey] < policy.Monthly {
			protected[b.Filename] = true
			groupCounts[monthlyKey]++
		}
	}

	// Separate kept and unprotected backups.
	var unprotected []BackupMeta
	for _, b := range backups {
		if protected[b.Filename] {
			result.Kept = append(result.Kept, b.Filename)
		} else {
			unprotected = append(unprotected, b)
		}
	}

	// Delete unprotected backups from outputDir.
	for _, b := range unprotected {
		path := filepath.Join(outputDir, b.Filename)
		if err := os.Remove(path); err != nil {
			// Log but don't abort — keep the file in Kept since deletion failed.
			log.Printf("backup: delete %q: %v", path, err)
			result.Kept = append(result.Kept, b.Filename)
		} else {
			result.Removed = append(result.Removed, b.Filename)
		}
	}

	// Sort for deterministic output.
	sort.Strings(result.Kept)
	sort.Strings(result.Removed)

	return result, nil
}

// BackupConfig holds the full backup configuration including retention policy
// and output directory.
type BackupConfig struct {
	RetentionPolicy RetentionPolicy `json:"retention_policy"`
	OutputDir       string          `json:"output_dir"`
}

// DefaultBackupConfig returns a BackupConfig with sensible defaults:
// Daily=7, Weekly=4, Monthly=6, and empty OutputDir.
func DefaultBackupConfig() BackupConfig {
	return BackupConfig{
		RetentionPolicy: RetentionPolicy{
			Daily:   7,
			Weekly:  4,
			Monthly: 6,
		},
		OutputDir: "",
	}
}

// LoadBackupConfig reads a BackupConfig from filePath. If the file does not
// exist, it returns the default config. On JSON parse error, the error is
// wrapped with "backup: parse config".
func LoadBackupConfig(filePath string) (BackupConfig, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultBackupConfig(), nil
		}
		return BackupConfig{}, fmt.Errorf("backup: read config %q: %w", filePath, err)
	}

	var cfg BackupConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return BackupConfig{}, fmt.Errorf("backup: parse config: %w", err)
	}

	return cfg, nil
}

// SaveBackupConfig writes cfg to filePath as indented JSON. It creates parent
// directories as needed and uses atomic write (write to .tmp, then os.Rename).
// Any pre-existing .tmp file from a prior crash is overwritten.
func SaveBackupConfig(filePath string, cfg BackupConfig) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: marshal config: %w", err)
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("backup: create config dir %q: %w", dir, err)
	}

	tmpFile := filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return fmt.Errorf("backup: write config tmp %q: %w", tmpFile, err)
	}

	if err := os.Rename(tmpFile, filePath); err != nil {
		// Clean up the temp file on rename failure.
		os.Remove(tmpFile)
		return fmt.Errorf("backup: rename config %q → %q: %w", tmpFile, filePath, err)
	}

	return nil
}
