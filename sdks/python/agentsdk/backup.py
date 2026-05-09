"""Backup — timestamped zip archives with atomic write and collision safety.

Provides ``CreateBackup`` to generate collision-safe, timestamped zip archives
of specified items, ``ListBackups`` to scan an output directory for existing
backup files sorted newest-first, ``GFSRotate`` for grandfather-father-son
retention rotation, and ``BackupConfig`` for load/save of backup configuration.

Filename format: ``{prefix}-backup-YYYYMMDD-HHMMSS.zip``
On collision, appends incrementing suffix: ``-2``, ``-3``, ...

Zip entry paths use forward slashes for cross-platform compatibility.
Non-existent items in the items list are silently skipped.
The zip is written atomically via a ``.tmp`` file + ``os.replace()``.
"""

from __future__ import annotations

import glob
import json
import logging
import os
import zipfile
from dataclasses import asdict, dataclass
from datetime import datetime
from pathlib import Path, PurePosixPath
from typing import Any, Dict, List, Optional, Tuple

logger = logging.getLogger(__name__)


@dataclass
class BackupMeta:
    """Metadata about a backup archive."""

    filename: str  # Base filename, e.g. "myapp-backup-20250101-120000.zip"
    size: int  # File size in bytes
    created_at: Optional[datetime]  # Parsed from filename timestamp


def _parse_backup_filename(filename: str, prefix: str) -> Optional[datetime]:
    """Extract the timestamp from a backup filename.

    Formats: ``{prefix}-backup-YYYYMMDD-HHMMSS.zip``
             ``{prefix}-backup-YYYYMMDD-HHMMSS-N.zip``
    """
    expected_prefix = f"{prefix}-backup-"
    if not filename.startswith(expected_prefix):
        return None

    rest = filename[len(expected_prefix):]
    # Remove .zip extension.
    rest = rest.removesuffix(".zip")

    # Check for collision suffix: YYYYMMDD-HHMMSS-N
    ts_part = rest
    # "YYYYMMDD-HHMMSS" is 15 chars; any dash at index 15+ indicates a suffix.
    last_dash = rest.rfind("-")
    if last_dash >= 15:
        suffix_str = rest[last_dash + 1:]
        if suffix_str.isdigit():
            ts_part = rest[:last_dash]

    try:
        return datetime.strptime(ts_part, "%Y%m%d-%H%M%S")
    except ValueError:
        return None


def CreateBackup(
    data_dir: str, output_dir: str, prefix: str, items: List[str]
) -> Tuple[str, int]:
    """Generate a collision-safe, timestamped zip archive.

    Args:
        data_dir: Root directory containing the items to back up.
        output_dir: Directory where the zip archive will be written.
        prefix: Filename prefix (e.g. ``"myapp"``).
        items: List of file or directory names relative to *data_dir*.

    Returns:
        A ``(zip_path, size)`` tuple.

    Raises:
        OSError: If *data_dir* does not exist or the zip cannot be written.
        ValueError: If *data_dir* is not a directory.
    """
    # Validate data_dir exists.
    if not os.path.exists(data_dir):
        raise OSError(f"backup: stat data dir {data_dir!r}: No such file or directory")
    if not os.path.isdir(data_dir):
        raise ValueError(f"backup: data dir {data_dir!r} is not a directory")

    # Create output directory if needed.
    try:
        os.makedirs(output_dir, exist_ok=True)
    except OSError as e:
        raise OSError(f"backup: create output dir {output_dir!r}: {e}") from e

    # Generate base filename from current timestamp.
    now = datetime.now()
    ts_str = now.strftime("%Y%m%d-%H%M%S")
    base_name = f"{prefix}-backup-{ts_str}.zip"

    # Resolve collision by appending -2, -3, ...
    target = os.path.join(output_dir, base_name)
    final_target = target
    suffix = 2
    while os.path.exists(final_target):
        final_target = os.path.join(
            output_dir, f"{prefix}-backup-{ts_str}-{suffix}.zip"
        )
        suffix += 1

    # Atomic write: create .tmp file, then rename.
    tmp_path = final_target + ".tmp"

    try:
        with zipfile.ZipFile(tmp_path, "w", compression=zipfile.ZIP_DEFLATED) as zw:
            for item in items:
                src_path = os.path.join(data_dir, item)
                if not os.path.exists(src_path):
                    # Silently skip non-existent items.
                    continue

                if os.path.isdir(src_path):
                    # Walk the directory tree.
                    for dirpath, dirnames, filenames in os.walk(src_path):
                        for fname in filenames:
                            full_path = os.path.join(dirpath, fname)
                            if not os.path.isfile(full_path):
                                # Skip non-regular files (symlinks, etc.)
                                continue
                            rel = os.path.relpath(full_path, data_dir)
                            zip_name = str(PurePosixPath(rel))
                            try:
                                zw.write(full_path, zip_name)
                            except OSError as e:
                                raise OSError(
                                    f"backup: add file {full_path!r}: {e}"
                                ) from e
                elif os.path.isfile(src_path):
                    zip_name = str(PurePosixPath(item))
                    try:
                        zw.write(src_path, zip_name)
                    except OSError as e:
                        raise OSError(
                            f"backup: add file {src_path!r}: {e}"
                        ) from e
                # Skip symlinks and other non-file, non-dir items silently.
    except Exception:
        # Clean up temp file on failure.
        try:
            os.remove(tmp_path)
        except OSError:
            pass
        raise

    # Atomic rename from .tmp to final target.
    try:
        os.replace(tmp_path, final_target)
    except OSError as e:
        try:
            os.remove(tmp_path)
        except OSError:
            pass
        raise OSError(
            f"backup: rename {tmp_path!r} to {final_target!r}: {e}"
        ) from e

    # Read back size.
    final_info = os.stat(final_target)
    return final_target, final_info.st_size


def ListBackups(output_dir: str, prefix: str) -> List[BackupMeta]:
    """Scan *output_dir* for backup files matching *prefix*.

    Returns metadata sorted by ``created_at`` descending (newest first).
    Returns an empty list if no backups are found or the directory does not
    exist.

    Args:
        output_dir: Directory containing backup zip files.
        prefix: Filename prefix to filter on.

    Returns:
        A list of :class:`BackupMeta`, always non-None (empty list if none found).
    """
    metas: List[BackupMeta] = []

    pattern = os.path.join(output_dir, f"{prefix}-backup-*.zip")
    matches = glob.glob(pattern)

    for m in matches:
        try:
            info = os.stat(m)
        except OSError:
            continue

        meta = BackupMeta(
            filename=os.path.basename(m),
            size=info.st_size,
            created_at=_parse_backup_filename(os.path.basename(m), prefix),
        )
        metas.append(meta)

    # Sort by created_at descending (newest first).
    # Items with None created_at go to the end.
    metas.sort(
        key=lambda b: b.created_at or datetime.min,
        reverse=True,
    )

    return metas


# ---------------------------------------------------------------------------
# GFS (Grandfather-Father-Son) rotation
# ---------------------------------------------------------------------------


@dataclass
class RetentionPolicy:
    """Defines how many backups to retain per time granularity."""

    daily: int = 0   # Number of newest backups to keep per calendar day.
    weekly: int = 0  # Number of newest backups to keep per ISO week.
    monthly: int = 0  # Number of newest backups to keep per calendar month.


@dataclass
class RotationResult:
    """Reports which backups were kept and which were removed by GFSRotate."""

    kept: List[str]  # Filenames of protected (kept) backups.
    removed: List[str]  # Filenames of successfully deleted backups.


def GFSRotate(
    backups: List[BackupMeta], policy: RetentionPolicy, output_dir: str
) -> RotationResult:
    """Perform grandfather-father-son rotation on the given backups.

    Backups are expected to be sorted newest-first (as returned by
    ``ListBackups``). For each retention rule (daily, weekly, monthly), the N
    newest backups within each group (calendar day, ISO week, calendar month)
    are marked as protected. A backup protected by ANY rule is kept.

    Unprotected backups are deleted from *output_dir*. Individual deletion
    failures are logged but do not halt rotation — the failed file remains in
    the Kept list.

    Args:
        backups: List of :class:`BackupMeta` sorted newest-first.
        policy: Retention policy defining daily/weekly/monthly limits.
        output_dir: Directory containing the backup files.

    Returns:
        A :class:`RotationResult` with kept and removed filenames.

    Raises:
        OSError: If *output_dir* does not exist or is not a directory.
    """
    # Validate output_dir.
    if not os.path.exists(output_dir):
        raise OSError(
            f"backup: stat output dir {output_dir!r}: "
            "No such file or directory"
        )
    if not os.path.isdir(output_dir):
        raise OSError(
            f"backup: output dir {output_dir!r} is not a directory"
        )

    result = RotationResult(kept=[], removed=[])

    if not backups:
        return result

    # Build protected set: filename → True.
    protected: Dict[str, bool] = {}

    # Group keys and counters.
    # group_key = (rule, group_string) → count of protected backups so far.
    group_counts: Dict[Tuple[str, str], int] = {}

    for b in backups:
        if b.created_at is None:
            continue

        iso_cal = b.created_at.isocalendar()
        iso_year = iso_cal[0]
        iso_week = iso_cal[1]
        month = b.created_at.strftime("%Y%m")
        day_key = b.created_at.strftime("%Y%m%d")
        week_key = f"{iso_year:04d}-W{iso_week:02d}"

        # Check each rule; a backup is protected if ANY rule protects it.
        if policy.daily > 0:
            dk = ("daily", day_key)
            if group_counts.get(dk, 0) < policy.daily:
                protected[b.filename] = True
                group_counts[dk] = group_counts.get(dk, 0) + 1

        if policy.weekly > 0:
            wk = ("weekly", week_key)
            if group_counts.get(wk, 0) < policy.weekly:
                protected[b.filename] = True
                group_counts[wk] = group_counts.get(wk, 0) + 1

        if policy.monthly > 0:
            mk = ("monthly", month)
            if group_counts.get(mk, 0) < policy.monthly:
                protected[b.filename] = True
                group_counts[mk] = group_counts.get(mk, 0) + 1

    # Separate kept and unprotected backups.
    unprotected: List[BackupMeta] = []
    for b in backups:
        if protected.get(b.filename):
            result.kept.append(b.filename)
        else:
            unprotected.append(b)

    # Delete unprotected backups from output_dir.
    for b in unprotected:
        path = os.path.join(output_dir, b.filename)
        try:
            os.remove(path)
            result.removed.append(b.filename)
        except OSError:
            # Log but don't abort — keep the file in Kept since deletion failed.
            logger.warning("backup: delete %r: failed", path)
            result.kept.append(b.filename)

    # Sort for deterministic output.
    result.kept.sort()
    result.removed.sort()

    return result


# ---------------------------------------------------------------------------
# Backup configuration
# ---------------------------------------------------------------------------


@dataclass
class BackupConfig:
    """Full backup configuration including retention policy and output directory."""

    retention_policy: RetentionPolicy
    output_dir: str


def DefaultBackupConfig() -> BackupConfig:
    """Return a :class:`BackupConfig` with sensible defaults.

    Daily=7, Weekly=4, Monthly=6, and empty OutputDir.
    """
    return BackupConfig(
        retention_policy=RetentionPolicy(daily=7, weekly=4, monthly=6),
        output_dir="",
    )


def LoadBackupConfig(file_path: str) -> BackupConfig:
    """Read a :class:`BackupConfig` from *file_path*.

    If the file does not exist, returns the default config.
    On JSON parse error, raises an OSError with ``backup: parse config`` prefix.

    Args:
        file_path: Path to the JSON configuration file.

    Returns:
        A :class:`BackupConfig`.

    Raises:
        OSError: On read or parse errors (except missing file → defaults).
    """
    try:
        with open(file_path, "r", encoding="utf-8") as f:
            data = json.load(f)
    except FileNotFoundError:
        return DefaultBackupConfig()
    except OSError as e:
        raise OSError(f"backup: read config {file_path!r}: {e}") from e
    except json.JSONDecodeError as e:
        raise OSError(f"backup: parse config: {e}") from e

    # Map JSON keys to dataclass fields.
    rp_data = data.get("retention_policy", {})
    retention = RetentionPolicy(
        daily=rp_data.get("daily", 7),
        weekly=rp_data.get("weekly", 4),
        monthly=rp_data.get("monthly", 6),
    )

    return BackupConfig(
        retention_policy=retention,
        output_dir=data.get("output_dir", ""),
    )


def SaveBackupConfig(file_path: str, config: BackupConfig) -> None:
    """Write *config* to *file_path* as indented JSON.

    Creates parent directories as needed and uses atomic write
    (write to ``.tmp``, then ``os.replace``). Any pre-existing ``.tmp``
    file from a prior crash is overwritten.

    Args:
        file_path: Destination path for the JSON file.
        config: The :class:`BackupConfig` to serialize.

    Raises:
        OSError: On write, directory creation, or rename failure.
    """
    # Build the JSON-serializable dict matching Go SDK's field names.
    obj: Dict[str, Any] = {
        "retention_policy": {
            "daily": config.retention_policy.daily,
            "weekly": config.retention_policy.weekly,
            "monthly": config.retention_policy.monthly,
        },
        "output_dir": config.output_dir,
    }

    try:
        data = json.dumps(obj, indent=2)
    except (TypeError, ValueError) as e:
        raise OSError(f"backup: marshal config: {e}") from e

    # Create parent directories.
    parent = os.path.dirname(file_path)
    try:
        os.makedirs(parent, exist_ok=True)
    except OSError as e:
        raise OSError(f"backup: create config dir {parent!r}: {e}") from e

    # Atomic write: .tmp + os.replace.
    tmp_file = file_path + ".tmp"
    try:
        with open(tmp_file, "w", encoding="utf-8") as f:
            f.write(data)
    except OSError as e:
        raise OSError(f"backup: write config tmp {tmp_file!r}: {e}") from e

    try:
        os.replace(tmp_file, file_path)
    except OSError as e:
        try:
            os.remove(tmp_file)
        except OSError:
            pass
        raise OSError(
            f"backup: rename config {tmp_file!r} → {file_path!r}: {e}"
        ) from e
