"""Restore — extract files from zip archives with zip-slip protection and CRC-32 verification.

Provides ``RestoreBackup`` to extract files from a zip archive created by
``CreateBackup`` into a target directory, with support for selective restore
and full integrity verification.

Security: zip-slip path traversal is detected and rejected for every entry.
Integrity: CRC-32 is verified by reading the full entry content before writing.
"""

from __future__ import annotations

import os
import zipfile
from dataclasses import dataclass, field
from typing import List, Optional


@dataclass
class RestoreResult:
    """Holds the outcome of a RestoreBackup call."""

    restored: List[str] = field(default_factory=list)
    """Paths (relative to targetDir) of successfully restored files."""

    skipped: List[str] = field(default_factory=list)
    """Paths of zip entries that were skipped (e.g. not in items filter)."""


def RestoreBackup(
    zip_path: str,
    target_dir: str,
    items: Optional[List[str]] = None,
) -> RestoreResult:
    """Extract files from a zip archive into *target_dir*.

    Parameters:
        zip_path: Path to the zip archive.
        target_dir: Directory to extract files into (created if it does not exist).
        items: Optional list of entry names to restore. When ``None`` or empty,
            all entries are restored. When non-empty, only entries whose name
            exactly matches an item or has an item as a directory prefix are
            restored.

    Returns:
        A :class:`RestoreResult` with restored file paths.

    Raises:
        OSError: On the first error during extraction, aborts and returns
            the result together with any files already restored.
    """
    result = RestoreResult()

    # Open the zip archive.
    try:
        zf = zipfile.ZipFile(zip_path, "r")
    except (OSError, zipfile.BadZipFile) as e:
        raise OSError(f"restore: open zip {zip_path!r}: {e}") from e

    with zf:
        # Ensure target_dir exists.
        try:
            os.makedirs(target_dir, exist_ok=True)
        except OSError as e:
            raise OSError(
                f"restore: create target dir {target_dir!r}: {e}"
            ) from e

        # Build item lookup set for selective restore.
        selective = bool(items)
        item_set: set = set(items) if items else set()

        # Normalise target_dir to an absolute, clean path for zip-slip checks.
        try:
            abs_target = os.path.realpath(target_dir)
        except OSError as e:
            raise OSError(
                f"restore: resolve target dir {target_dir!r}: {e}"
            ) from e

        for info in zf.infolist():
            # Directory entries — create them but don't count as restored files.
            if info.is_dir():
                if selective and not _matches_item(info.filename, item_set):
                    result.skipped.append(info.filename)
                    continue
                if _is_zip_slip(info.filename, abs_target):
                    raise OSError(
                        f"restore: zip-slip detected for directory entry "
                        f"{info.filename!r}"
                    )
                dir_path = os.path.join(abs_target, info.filename)
                try:
                    os.makedirs(dir_path, exist_ok=True)
                except OSError as e:
                    raise OSError(
                        f"restore: create directory {dir_path!r}: {e}"
                    ) from e
                continue

            # Selective restore filter: skip non-matching files.
            if selective and not _matches_item(info.filename, item_set):
                result.skipped.append(info.filename)
                continue

            # Zip-slip check.
            if _is_zip_slip(info.filename, abs_target):
                raise OSError(
                    f"restore: zip-slip detected for entry {info.filename!r}"
                )

            dest_path = os.path.join(abs_target, info.filename)

            try:
                _restore_file(zf, info, dest_path)
            except OSError as e:
                raise OSError(
                    f"restore: extract {info.filename!r}: {e}"
                ) from e

            # Record relative path using forward slashes (matching zip entry convention).
            result.restored.append(info.filename)

    return result


def _matches_item(entry_name: str, item_set: set) -> bool:
    """Check whether a zip entry name should be restored during selective restore.

    An entry matches if:
    - Its name exactly equals an item (e.g. "config.json" == "config.json").
    - An item is a directory prefix of the entry (e.g. item "data/" matches
      entry "data/file.txt").
    """
    if entry_name in item_set:
        return True
    # Check directory-prefix match: item "data" should match "data/file.txt".
    for item in item_set:
        prefix = item.rstrip("/") + "/"
        if entry_name.startswith(prefix):
            return True
    return False


def _is_zip_slip(entry_path: str, target_dir: str) -> bool:
    """Return True if *entry_path* would resolve outside *target_dir* (path traversal)."""
    # Normalise both paths and ensure the resolved path is within target_dir.
    try:
        # Use os.path.normpath to collapse any ".." components.
        dest = os.path.normpath(os.path.join(target_dir, entry_path))
        abs_dest = os.path.realpath(dest)
        abs_target = os.path.realpath(target_dir)
    except OSError:
        return True  # treat resolution failure as suspicious

    # Ensure the resolved path starts with the target dir + separator.
    target_prefix = abs_target + os.sep
    if not abs_dest.startswith(target_prefix) and abs_dest != abs_target:
        return True
    return False


def _restore_file(
    zf: zipfile.ZipFile,
    info: zipfile.ZipInfo,
    dest_path: str,
) -> None:
    """Extract a single zip entry to *dest_path* with CRC-32 verification.

    Reads the full entry content first (which verifies CRC-32 via
    ``ZipFile.read``), then writes to the destination file. This mirrors the
    Go implementation's read-then-write pattern for integrity verification.
    """
    # Create parent directories.
    parent = os.path.dirname(dest_path)
    try:
        os.makedirs(parent, exist_ok=True)
    except OSError as e:
        raise OSError(f"create parent dirs: {e}") from e

    # Read the full entry content — ZipFile.read() verifies CRC-32.
    try:
        data = zf.read(info.filename)
    except (OSError, zipfile.BadZipFile) as e:
        raise OSError(f"read entry: {e}") from e

    # Write to destination file.
    try:
        with open(dest_path, "wb") as out:
            out.write(data)
    except OSError as e:
        raise OSError(f"write file: {e}") from e
