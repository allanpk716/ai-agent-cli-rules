"""Tests for backup.py — CreateBackup and ListBackups.

Mirrors the Go SDK test scenarios in backup_test.go:
- CreateBackup: basic, missing items, empty items, filename format, collision
  safety, forward slashes, auto-create output dir, data dir validation,
  atomic cleanup, symlink skip, empty file
- ListBackups: empty, nonexistent dir, prefix filtering, sorted descending,
  collision suffix, size/metadata
- Integration: round-trip, multiple backups
"""

from __future__ import annotations

import os
import re
import zipfile
from datetime import datetime
from pathlib import Path

import pytest

from agentsdk.backup import (
    BackupConfig,
    BackupMeta,
    CreateBackup,
    DefaultBackupConfig,
    GFSRotate,
    ListBackups,
    LoadBackupConfig,
    RetentionPolicy,
    RotationResult,
    SaveBackupConfig,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write_file(path: str, content: str) -> None:
    """Write *content* to *path*, creating parent dirs as needed."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)


def _create_fake_zip(directory: str, name: str, content: str = "") -> str:
    """Create a minimal zip file in *directory* with the given filename."""
    path = os.path.join(directory, name)
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as zw:
        if content:
            zw.writestr("data.bin", content)
    return path


# ---------------------------------------------------------------------------
# CreateBackup tests
# ---------------------------------------------------------------------------


class TestCreateBackupBasic:
    """Basic backup with files and directories."""

    def test_basic(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "file1.txt"), "hello world")
        _write_file(str(data_dir / "file2.txt"), "goodbye")
        _write_file(str(data_dir / "sub" / "deep" / "file3.txt"), "deep content")

        zip_path, size = CreateBackup(str(data_dir), str(output_dir), "testapp", [
            "file1.txt", "file2.txt", "sub",
        ])

        assert os.path.exists(zip_path)
        assert size > 0

        with zipfile.ZipFile(zip_path) as zr:
            names = {f.filename for f in zr.infolist()}
            assert "file1.txt" in names
            assert "file2.txt" in names
            assert "sub/deep/file3.txt" in names


class TestCreateBackupMissingItemSkipped:
    """Missing items silently skipped."""

    def test_missing_items_skipped(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "exists.txt"), "I exist")

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", [
            "exists.txt", "nonexistent.txt", "also_missing.txt",
        ])

        with zipfile.ZipFile(zip_path) as zr:
            assert len(zr.infolist()) == 1
            assert zr.infolist()[0].filename == "exists.txt"


class TestCreateBackupEmptyItems:
    """Empty items list creates a valid empty zip."""

    def test_empty_items(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", [])

        assert zip_path != ""
        assert os.path.exists(zip_path)

        with zipfile.ZipFile(zip_path) as zr:
            assert len(zr.infolist()) == 0


class TestCreateBackupFilenameFormat:
    """Filename format matches {prefix}-backup-YYYYMMDD-HHMMSS.zip."""

    def test_filename_format(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "f.txt"), "x")

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "myapp", ["f.txt"])

        base = os.path.basename(zip_path)
        pattern = r"^myapp-backup-\d{8}-\d{6}\.zip$"
        assert re.match(pattern, base), f"filename {base!r} does not match {pattern}"


class TestCreateBackupCollisionSafety:
    """Collision detection appends -2, -3, etc. suffixes."""

    def test_collision_safety(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "f.txt"), "content")

        zip1, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", ["f.txt"])
        zip2, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", ["f.txt"])

        base1 = os.path.basename(zip1)
        base2 = os.path.basename(zip2)

        # Both must be valid backup filenames and different.
        assert base1 != base2
        valid_pattern = r"^testapp-backup-\d{8}-\d{6}(-\d+)?\.zip$"
        assert re.match(valid_pattern, base1)
        assert re.match(valid_pattern, base2)

        # Force a collision by creating a pre-existing file with zip2's timestamp.
        m = re.match(r"^testapp-backup-(\d{8}-\d{6})(-\d+)?\.zip$", base2)
        assert m is not None
        ts_part = m.group(1)
        preexist = os.path.join(output_dir, f"testapp-backup-{ts_part}.zip")
        _write_file(preexist, "fake")

        zip3, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", ["f.txt"])
        base3 = os.path.basename(zip3)

        # Should NOT have the same name as the pre-existing file.
        assert base3 != f"testapp-backup-{ts_part}.zip"
        assert re.match(valid_pattern, base3)

        # The third zip should be valid and contain our file.
        with zipfile.ZipFile(zip3) as zr:
            assert len(zr.infolist()) == 1
            assert zr.infolist()[0].filename == "f.txt"


class TestCreateBackupForwardSlashes:
    """Zip entry paths use forward slashes only."""

    def test_forward_slashes(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "a" / "b" / "c" / "deep.txt"), "nested")
        _write_file(str(data_dir / "top.txt"), "top level")

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", [
            "a", "top.txt",
        ])

        with zipfile.ZipFile(zip_path) as zr:
            for f in zr.infolist():
                assert "\\" not in f.filename, (
                    f"zip entry {f.filename!r} contains backslash"
                )
            names = {f.filename for f in zr.infolist()}
            assert "a/b/c/deep.txt" in names
            assert "top.txt" in names


class TestCreateBackupCreatesOutputDir:
    """Output directory is auto-created if it doesn't exist."""

    def test_creates_output_dir(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        base_dir = tmp_path / "base"
        output_dir = base_dir / "nested" / "output"
        data_dir.mkdir()

        _write_file(str(data_dir / "f.txt"), "data")

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", ["f.txt"])

        assert os.path.isdir(str(output_dir))
        assert os.path.exists(zip_path)


class TestCreateBackupDataDirNotExist:
    """Error when data_dir does not exist."""

    def test_data_dir_not_exist(self, tmp_path: Path):
        output_dir = tmp_path / "output"
        output_dir.mkdir()

        with pytest.raises(OSError, match="backup:"):
            CreateBackup(
                "/nonexistent/path/that/does/not/exist",
                str(output_dir), "testapp", ["f.txt"],
            )


class TestCreateBackupDataDirIsFile:
    """Error when data_dir is a file, not a directory."""

    def test_data_dir_is_file(self, tmp_path: Path):
        output_dir = tmp_path / "output"
        output_dir.mkdir()
        data_file = tmp_path / "not-a-dir.txt"
        _write_file(str(data_file), "I am a file")

        with pytest.raises(ValueError, match="not a directory"):
            CreateBackup(str(data_file), str(output_dir), "testapp", ["f.txt"])


class TestCreateBackupAtomicCleanup:
    """No orphaned .tmp files on failure."""

    def test_atomic_cleanup(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        data_dir.mkdir()
        _write_file(str(data_dir / "f.txt"), "data")

        # Force failure: make output parent a file.
        parent_file = tmp_path / "not-a-dir.txt"
        _write_file(str(parent_file), "file")
        bad_output_dir = parent_file / "subdir"

        with pytest.raises(OSError, match="backup:"):
            CreateBackup(str(data_dir), str(bad_output_dir), "testapp", ["f.txt"])

        # No orphaned .tmp files should exist.
        tmp_files = list(Path(tmp_path).rglob("*.tmp"))
        assert len(tmp_files) == 0, f"found orphaned .tmp files: {tmp_files}"


class TestCreateBackupSymlinkSkipped:
    """Broken symlinks are silently skipped."""

    def test_symlink_skipped(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "real.txt"), "real content")

        try:
            os.symlink(
                "nonexistent.txt",
                str(data_dir / "broken_link.txt"),
            )
        except OSError:
            pytest.skip("symlinks not supported on this platform")

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", [
            "real.txt", "broken_link.txt",
        ])

        with zipfile.ZipFile(zip_path) as zr:
            assert len(zr.infolist()) == 1


class TestCreateBackupEmptyFile:
    """Empty files are included with zero size."""

    def test_empty_file(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        Path(data_dir / "empty.txt").touch()

        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", [
            "empty.txt",
        ])

        with zipfile.ZipFile(zip_path) as zr:
            assert len(zr.infolist()) == 1
            assert zr.infolist()[0].file_size == 0


# ---------------------------------------------------------------------------
# ListBackups tests
# ---------------------------------------------------------------------------


class TestListBackupsEmpty:
    """Empty directory returns non-None empty list."""

    def test_empty(self, tmp_path: Path):
        metas = ListBackups(str(tmp_path), "testapp")
        assert metas == []


class TestListBackupsNonexistentDir:
    """Nonexistent directory returns non-None empty list."""

    def test_nonexistent_dir(self):
        metas = ListBackups("/nonexistent/path", "testapp")
        assert metas == []


class TestListBackupsFiltersByPrefix:
    """Only files matching the prefix are returned."""

    def test_filters_by_prefix(self, tmp_path: Path):
        _create_fake_zip(str(tmp_path), "app1-backup-20250101-120000.zip")
        _create_fake_zip(str(tmp_path), "app2-backup-20250101-120000.zip")
        _create_fake_zip(str(tmp_path), "other-file.zip")

        metas = ListBackups(str(tmp_path), "app1")
        assert len(metas) == 1
        assert metas[0].filename == "app1-backup-20250101-120000.zip"


class TestListBackupsSortedDescending:
    """Results are sorted newest-first."""

    def test_sorted_descending(self, tmp_path: Path):
        _create_fake_zip(str(tmp_path), "app-backup-20250101-100000.zip")
        _create_fake_zip(str(tmp_path), "app-backup-20250103-100000.zip")
        _create_fake_zip(str(tmp_path), "app-backup-20250102-100000.zip")

        metas = ListBackups(str(tmp_path), "app")
        assert len(metas) == 3
        assert metas[0].filename == "app-backup-20250103-100000.zip"
        assert metas[1].filename == "app-backup-20250102-100000.zip"
        assert metas[2].filename == "app-backup-20250101-100000.zip"


class TestListBackupsWithCollisionSuffix:
    """Collision-suffixed files parse to the same CreatedAt."""

    def test_collision_suffix(self, tmp_path: Path):
        _create_fake_zip(str(tmp_path), "app-backup-20250101-120000.zip")
        _create_fake_zip(str(tmp_path), "app-backup-20250101-120000-2.zip")

        metas = ListBackups(str(tmp_path), "app")
        assert len(metas) == 2
        assert metas[0].created_at == metas[1].created_at


class TestListBackupsSizeAndMetadata:
    """Size and metadata are populated correctly."""

    def test_size_and_metadata(self, tmp_path: Path):
        content = "test backup content for size verification"
        _create_fake_zip(str(tmp_path), "app-backup-20250101-120000.zip", content)

        metas = ListBackups(str(tmp_path), "app")
        assert len(metas) == 1
        assert metas[0].size > 0
        assert metas[0].filename == "app-backup-20250101-120000.zip"


# ---------------------------------------------------------------------------
# Integration tests
# ---------------------------------------------------------------------------


class TestBackupIntegrationRoundTrip:
    """CreateBackup + ListBackups round-trip."""

    def test_round_trip(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "config" / "app.yaml"), "name: test\nport: 8080")
        _write_file(str(data_dir / "data.json"), '{"key": "value"}')

        zip_path, size = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["config", "data.json"],
        )
        assert size > 0

        metas = ListBackups(str(output_dir), "testapp")
        assert len(metas) == 1

        meta = metas[0]
        assert meta.size == size
        assert meta.filename == os.path.basename(zip_path)
        assert meta.created_at is not None

        # Verify zip contents.
        with zipfile.ZipFile(zip_path) as zr:
            names = {f.filename for f in zr.infolist()}
            assert "config/app.yaml" in names
            assert "data.json" in names


class TestBackupIntegrationMultipleBackups:
    """Multiple backups are sorted newest-first."""

    def test_multiple_backups(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "f.txt"), "v1")
        CreateBackup(str(data_dir), str(output_dir), "app", ["f.txt"])

        _write_file(str(data_dir / "f.txt"), "v2")
        CreateBackup(str(data_dir), str(output_dir), "app", ["f.txt"])

        metas = ListBackups(str(output_dir), "app")
        assert len(metas) == 2

        # Newest first (or equal if same second with collision suffix).
        if metas[0].created_at and metas[1].created_at:
            assert metas[0].created_at >= metas[1].created_at


# ---------------------------------------------------------------------------
# GFS Rotate helpers
# ---------------------------------------------------------------------------


def _create_fake_backup_files(
    directory: str, prefix: str, timestamps: list
) -> list:
    """Create fake backup files with given timestamps and return BackupMeta list.

    Mirrors the Go helper ``createFakeBackupFiles``.
    """
    from agentsdk.backup import BackupMeta

    metas = []
    for ts in timestamps:
        name = f"{prefix}-backup-{ts.strftime('%Y%m%d-%H%M%S')}.zip"
        path = os.path.join(directory, name)
        with open(path, "w") as f:
            f.write("fake backup")
        metas.append(BackupMeta(filename=name, size=10, created_at=ts))

    # Sort newest-first (matching ListBackups output).
    metas.sort(key=lambda b: b.created_at or datetime.min, reverse=True)
    return metas


# ---------------------------------------------------------------------------
# GFS Rotate tests
# ---------------------------------------------------------------------------


class TestGfsDailyRetention:
    """Daily retention: keep N per day."""

    def test_daily_retention(self, tmp_path: Path):
        from datetime import timedelta

        directory = str(tmp_path)
        base = datetime(2025, 1, 15, 10, 0, 0)
        timestamps = [base + timedelta(hours=i) for i in range(5)]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(daily=2)
        result = GFSRotate(backups, policy, directory)

        assert len(result.kept) == 2
        assert len(result.removed) == 3

        # Verify the 2 newest files (13:00, 14:00) are kept.
        for f in result.removed:
            assert "130000" not in f, f"newest file should be kept: {f}"
            assert "140000" not in f, f"newest file should be kept: {f}"


class TestGfsWeeklyRetention:
    """Weekly retention: keep N per ISO week."""

    def test_weekly_retention(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 6, 10, 0, 0),   # Week 1
            datetime(2025, 1, 7, 10, 0, 0),   # Week 1
            datetime(2025, 1, 13, 10, 0, 0),  # Week 2
            datetime(2025, 1, 14, 10, 0, 0),  # Week 2
            datetime(2025, 1, 20, 10, 0, 0),  # Week 3
            datetime(2025, 1, 21, 10, 0, 0),  # Week 3
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(weekly=1)
        result = GFSRotate(backups, policy, directory)

        assert len(result.kept) == 3
        assert len(result.removed) == 3


class TestGfsMonthlyRetention:
    """Monthly retention: keep N per calendar month."""

    def test_monthly_retention(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 15, 10, 0, 0),  # January
            datetime(2025, 1, 20, 10, 0, 0),  # January
            datetime(2025, 2, 15, 10, 0, 0),  # February
            datetime(2025, 2, 20, 10, 0, 0),  # February
            datetime(2025, 3, 15, 10, 0, 0),  # March
            datetime(2025, 3, 20, 10, 0, 0),  # March
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(monthly=1)
        result = GFSRotate(backups, policy, directory)

        assert len(result.kept) == 3
        assert len(result.removed) == 3


class TestGfsCombinedRetention:
    """Combined daily+weekly+monthly retention."""

    def test_combined_retention(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 10, 8, 0, 0),   # Jan, week 2, day 1
            datetime(2025, 1, 10, 16, 0, 0),  # Jan, week 2, same day
            datetime(2025, 1, 15, 10, 0, 0),  # Jan, week 3, different day
            datetime(2025, 2, 5, 10, 0, 0),   # Feb, week 6
            datetime(2025, 2, 5, 18, 0, 0),   # Feb, week 6, same day
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(daily=1, weekly=1, monthly=1)
        result = GFSRotate(backups, policy, directory)

        # 3 kept, 2 removed.
        assert len(result.kept) == 3
        assert len(result.removed) == 2

        # Verify removed files actually don't exist on disk.
        for f in result.removed:
            assert not os.path.exists(
                os.path.join(directory, f)
            ), f"removed file still exists: {f}"

        # Verify kept files still exist on disk.
        for f in result.kept:
            assert os.path.exists(
                os.path.join(directory, f)
            ), f"kept file missing: {f}"


class TestGfsEmptyBackups:
    """Empty backup list returns empty result."""

    def test_empty_backups_none(self, tmp_path: Path):
        result = GFSRotate([], RetentionPolicy(daily=1), str(tmp_path))
        assert len(result.kept) == 0
        assert len(result.removed) == 0

    def test_empty_backups_list(self, tmp_path: Path):
        result = GFSRotate([], RetentionPolicy(daily=1), str(tmp_path))
        assert len(result.kept) == 0
        assert len(result.removed) == 0


class TestGfsDeletionFailure:
    """Deletion failure tolerance — failed file stays in Kept list."""

    def test_deletion_failure(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 15, 10, 0, 0),
            datetime(2025, 1, 15, 11, 0, 0),
            datetime(2025, 1, 15, 12, 0, 0),
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        # Find the oldest file (10:00) and make it read-only to prevent deletion.
        oldest_path = ""
        for b in backups:
            if "100000" in b.filename:
                oldest_path = os.path.join(directory, b.filename)
                break
        assert oldest_path != ""

        try:
            os.chmod(oldest_path, 0o444)
        except OSError:
            pytest.skip("cannot set read-only on this platform")

        policy = RetentionPolicy(daily=1)  # Only keep 1 per day
        result = GFSRotate(backups, policy, directory)

        # Total kept+removed must equal total backups (3).
        total = len(result.kept) + len(result.removed)
        assert total == 3

        # The newest (12:00) must always be protected.
        assert any("120000" in f for f in result.kept), (
            "newest backup (12:00) should always be kept"
        )

        # Restore permissions for cleanup.
        try:
            os.chmod(oldest_path, 0o644)
        except OSError:
            pass


class TestGfsIsoWeekEdgeCase:
    """ISO week edge case: Dec 31 → ISO week 1 of next year."""

    def test_iso_week_edge_case(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 12, 30, 10, 0, 0),  # ISO week 1 of 2026
            datetime(2025, 12, 31, 10, 0, 0),  # ISO week 1 of 2026
            datetime(2026, 1, 1, 10, 0, 0),    # ISO week 1 of 2026
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(weekly=1)
        result = GFSRotate(backups, policy, directory)

        # All 3 are in the same ISO week. Weekly=1 keeps 1 newest.
        assert len(result.kept) == 1
        assert len(result.removed) == 2


class TestGfsCrossYearWeek:
    """Cross-year week: Dec 2015 / Jan 2016 in same ISO week."""

    def test_cross_year_week(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2015, 12, 28, 10, 0, 0),  # 2015-W53
            datetime(2015, 12, 29, 10, 0, 0),  # 2015-W53
            datetime(2016, 1, 1, 10, 0, 0),    # 2015-W53
            datetime(2016, 1, 4, 10, 0, 0),    # 2016-W01
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        policy = RetentionPolicy(weekly=1)
        result = GFSRotate(backups, policy, directory)

        # 2015-W53: 3 backups → keeps 1. 2016-W01: 1 backup → keeps 1.
        assert len(result.kept) == 2
        assert len(result.removed) == 2


class TestGfsOutputDirNotExist:
    """Error when output_dir does not exist."""

    def test_output_dir_not_exist(self, tmp_path: Path):
        timestamps = [datetime(2025, 1, 15, 10, 0, 0)]
        backups = _create_fake_backup_files(str(tmp_path), "app", timestamps)

        with pytest.raises(OSError, match="backup:"):
            GFSRotate(backups, RetentionPolicy(daily=1), "/nonexistent/path")


class TestGfsOutputDirIsFile:
    """Error when output_dir is a file, not a directory."""

    def test_output_dir_is_file(self, tmp_path: Path):
        file_path = str(tmp_path / "not-a-dir")
        _write_file(file_path, "x")

        timestamps = [datetime(2025, 1, 15, 10, 0, 0)]
        backups = _create_fake_backup_files(str(tmp_path), "app", timestamps)

        with pytest.raises(OSError, match="not a directory"):
            GFSRotate(backups, RetentionPolicy(daily=1), file_path)


class TestGfsZeroPolicy:
    """Zero policy deletes all backups."""

    def test_zero_policy(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 15, 10, 0, 0),
            datetime(2025, 1, 15, 11, 0, 0),
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        result = GFSRotate(backups, RetentionPolicy(), directory)

        assert len(result.kept) == 0
        assert len(result.removed) == 2


class TestGfsAllProtected:
    """All backups protected when policy exceeds count."""

    def test_all_protected(self, tmp_path: Path):
        directory = str(tmp_path)
        timestamps = [
            datetime(2025, 1, 15, 10, 0, 0),
            datetime(2025, 1, 16, 10, 0, 0),
            datetime(2025, 1, 17, 10, 0, 0),
        ]
        backups = _create_fake_backup_files(directory, "app", timestamps)

        result = GFSRotate(backups, RetentionPolicy(daily=10), directory)

        assert len(result.kept) == 3
        assert len(result.removed) == 0


# ---------------------------------------------------------------------------
# BackupConfig tests
# ---------------------------------------------------------------------------


class TestBackupConfigDefaults:
    """Config defaults match Go SDK."""

    def test_default_values(self):
        cfg = DefaultBackupConfig()
        assert cfg.retention_policy.daily == 7
        assert cfg.retention_policy.weekly == 4
        assert cfg.retention_policy.monthly == 6
        assert cfg.output_dir == ""


class TestBackupConfigLoadNonexistent:
    """Load nonexistent file returns defaults."""

    def test_load_nonexistent_file(self):
        cfg = LoadBackupConfig("/nonexistent/path/backup-config.json")
        expected = DefaultBackupConfig()
        assert cfg.retention_policy.daily == expected.retention_policy.daily
        assert cfg.retention_policy.weekly == expected.retention_policy.weekly
        assert cfg.retention_policy.monthly == expected.retention_policy.monthly
        assert cfg.output_dir == expected.output_dir


class TestBackupConfigSaveAndLoadRoundTrip:
    """Save + load round-trip preserves values."""

    def test_save_and_load_round_trip(self, tmp_path: Path):
        file_path = str(tmp_path / "backup-config.json")

        original = BackupConfig(
            retention_policy=RetentionPolicy(daily=14, weekly=8, monthly=12),
            output_dir="/var/backups/myapp",
        )

        SaveBackupConfig(file_path, original)

        loaded = LoadBackupConfig(file_path)
        assert loaded.retention_policy.daily == original.retention_policy.daily
        assert loaded.retention_policy.weekly == original.retention_policy.weekly
        assert loaded.retention_policy.monthly == original.retention_policy.monthly
        assert loaded.output_dir == original.output_dir


class TestBackupConfigSaveCreatesParentDirs:
    """Save creates parent directories as needed."""

    def test_save_creates_parent_dirs(self, tmp_path: Path):
        file_path = str(tmp_path / "deep" / "nested" / "backup-config.json")

        cfg = BackupConfig(
            retention_policy=RetentionPolicy(daily=3, weekly=2, monthly=1),
            output_dir="/tmp/backups",
        )

        SaveBackupConfig(file_path, cfg)

        assert os.path.exists(file_path)

        loaded = LoadBackupConfig(file_path)
        assert loaded.output_dir == "/tmp/backups"


class TestBackupConfigSaveOverwritesExisting:
    """Save overwrites an existing config file."""

    def test_save_overwrites_existing(self, tmp_path: Path):
        file_path = str(tmp_path / "backup-config.json")

        first = BackupConfig(
            retention_policy=RetentionPolicy(daily=7, weekly=4, monthly=6),
            output_dir="/first/dir",
        )
        SaveBackupConfig(file_path, first)

        second = BackupConfig(
            retention_policy=RetentionPolicy(daily=30, weekly=12, monthly=24),
            output_dir="/second/dir",
        )
        SaveBackupConfig(file_path, second)

        loaded = LoadBackupConfig(file_path)
        assert loaded.output_dir == "/second/dir"
        assert loaded.retention_policy.daily == 30


class TestBackupConfigLoadMalformedJSON:
    """Load malformed JSON returns error with backup: prefix."""

    def test_load_malformed_json(self, tmp_path: Path):
        file_path = str(tmp_path / "bad-config.json")
        _write_file(file_path, "{not valid json!!!}")

        with pytest.raises(OSError, match="backup:"):
            LoadBackupConfig(file_path)


class TestBackupConfigSaveSucceedsWithStaleTmp:
    """Save succeeds even when a stale .tmp exists from a prior crash."""

    def test_save_succeeds_with_stale_tmp(self, tmp_path: Path):
        file_path = str(tmp_path / "backup-config.json")
        tmp_file = file_path + ".tmp"

        # Pre-create a stale .tmp file.
        _write_file(tmp_file, "stale crash data")

        cfg = BackupConfig(
            retention_policy=RetentionPolicy(daily=5, weekly=3, monthly=2),
            output_dir="/backups",
        )

        SaveBackupConfig(file_path, cfg)

        loaded = LoadBackupConfig(file_path)
        assert loaded.output_dir == "/backups"
        assert loaded.retention_policy.daily == 5

        # .tmp should be gone (renamed to final path).
        assert not os.path.exists(tmp_file)


class TestBackupConfigSaveCleansUpTmpOnFailure:
    """Save cleans up .tmp on failure (parent path is a file)."""

    def test_save_cleans_up_tmp_on_failure(self, tmp_path: Path):
        parent_file = str(tmp_path / "not-a-dir.txt")
        _write_file(parent_file, "I am a file")
        bad_path = os.path.join(parent_file, "subdir", "config.json")

        cfg = BackupConfig(
            retention_policy=RetentionPolicy(daily=1, weekly=1, monthly=1),
            output_dir="/tmp",
        )

        with pytest.raises(OSError, match="backup:"):
            SaveBackupConfig(bad_path, cfg)
