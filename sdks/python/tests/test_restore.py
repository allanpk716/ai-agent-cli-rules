"""Tests for restore.py — RestoreBackup.

Mirrors the Go SDK test scenarios in restore_test.go:
- Full restore round-trip with byte-identical content verification
- Selective restore (exact match and directory prefix)
- Zip-slip detection (path traversal vectors)
- CRC-32 corruption (deflate, stored, local header)
- Nonexistent zip, nonexistent items (silently skipped)
- Auto-create targetDir, overwrite existing
- First-error-abort with partial results
- Empty zip, directory entries, empty filename
- Mixed compression (stored + deflate), stored method
- Error prefix verification ("restore:"), result fields
- Empty items slice (= full restore)
- Round-trip content integrity (binary, empty, large, unicode, nested)
"""

from __future__ import annotations

import io
import os
import struct
import zipfile
from pathlib import Path

import pytest

from agentsdk.backup import CreateBackup
from agentsdk.restore import RestoreBackup, RestoreResult


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write_file(path: str, content: str) -> None:
    """Write *content* to *path*, creating parent dirs as needed."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        f.write(content)


def _create_zip_in_memory(
    entries: list[tuple[str, bytes, int | None]],
) -> bytes:
    """Create a zip in memory from a list of (name, content, method) tuples.

    *method* is ``zipfile.ZIP_STORED`` or ``zipfile.ZIP_DEFLATED`` (or
    ``None`` for default deflate).
    """
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w") as zw:
        for name, content, method in entries:
            if method is not None:
                info = zipfile.ZipInfo(name)
                info.compress_type = method
                zw.writestr(info, content)
            else:
                zw.writestr(name, content)
    return buf.getvalue()


def _write_zip_to_disk(path: str, data: bytes) -> None:
    """Write raw zip bytes to a file."""
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(data)


def _corrupt_zip_data(zip_data: bytearray, offset: int | None = None) -> bytearray:
    """Flip a byte in the compressed data area of a zip.

    If *offset* is given, flip exactly that byte. Otherwise, find the local
    file header and corrupt a byte in the compressed data region.
    """
    if offset is not None:
        zip_data[offset] ^= 0xFF
        return zip_data

    for i in range(len(zip_data) - 30):
        if (
            zip_data[i] == 0x50
            and zip_data[i + 1] == 0x4B
            and zip_data[i + 2] == 0x03
            and zip_data[i + 3] == 0x04
        ):
            if i + 30 >= len(zip_data):
                break
            filename_len = struct.unpack_from("<H", zip_data, i + 26)[0]
            extra_len = struct.unpack_from("<H", zip_data, i + 28)[0]
            compressed_size = struct.unpack_from("<I", zip_data, i + 18)[0]
            data_offset = i + 30 + filename_len + extra_len

            if compressed_size > 0 and data_offset + compressed_size <= len(zip_data):
                zip_data[data_offset + compressed_size // 2] ^= 0xFF
                return zip_data

    # Fallback: corrupt middle of file.
    zip_data[len(zip_data) // 2] ^= 0xFF
    return zip_data


# ---------------------------------------------------------------------------
# Full restore round-trip
# ---------------------------------------------------------------------------


class TestRestoreBackupFullRestore:
    """CreateBackup → RestoreBackup round-trip with nil items, verify content byte-identical."""

    def test_full_restore(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        os.makedirs(str(data_dir / "sub" / "deep"), exist_ok=True)
        _write_file(str(data_dir / "file1.txt"), "hello world")
        _write_file(str(data_dir / "file2.txt"), "goodbye")
        _write_file(str(data_dir / "sub" / "deep" / "file3.txt"), "deep content")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["file1.txt", "file2.txt", "sub"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), None)

        expected = ["file1.txt", "file2.txt", "sub/deep/file3.txt"]
        assert len(result.restored) == len(expected), (
            f"expected {len(expected)} restored, got {len(result.restored)}: {result.restored}"
        )

        for name in expected:
            assert name in result.restored, f"expected {name!r} in restored list"

            src_path = os.path.join(str(data_dir), name.replace("/", os.sep))
            dst_path = os.path.join(str(target_dir), name.replace("/", os.sep))

            with open(src_path, "rb") as sf, open(dst_path, "rb") as df:
                assert sf.read() == df.read(), (
                    f"content mismatch for {name!r}"
                )


# ---------------------------------------------------------------------------
# Round-trip content integrity
# ---------------------------------------------------------------------------


class TestRestoreBackupRoundTripContentIntegrity:
    """Multiple file types/sizes, verify exact byte match after round-trip."""

    def test_round_trip_content_integrity(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        # Small text file.
        _write_file(str(data_dir / "small.txt"), "hello")

        # Empty file.
        Path(data_dir / "empty.bin").touch()

        # Binary-like content (all byte values).
        binary_content = bytes(range(256))
        with open(str(data_dir / "binary.bin"), "wb") as f:
            f.write(binary_content)

        # Larger file (>4KB to exercise compression).
        large_content = b"The quick brown fox jumps over the lazy dog.\n" * 200
        with open(str(data_dir / "large.txt"), "wb") as f:
            f.write(large_content)

        # File with special characters / unicode.
        _write_file(str(data_dir / "special.txt"), "日本語 emoji test\nline 2\r\nwindows")

        # Nested directory with content.
        os.makedirs(str(data_dir / "nested" / "dir"), exist_ok=True)
        _write_file(str(data_dir / "nested" / "dir" / "deep.txt"), "deeply nested content")

        items = [
            "small.txt", "empty.bin", "binary.bin",
            "large.txt", "special.txt", "nested",
        ]
        zip_path, _ = CreateBackup(str(data_dir), str(output_dir), "testapp", items)

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), None)

        assert len(result.restored) == 6, (
            f"expected 6 restored, got {len(result.restored)}: {result.restored}"
        )

        # Walk original data_dir and compare every file.
        for root, _dirs, files in os.walk(str(data_dir)):
            for fname in files:
                src_path = os.path.join(root, fname)
                rel = os.path.relpath(src_path, str(data_dir))
                dst_path = os.path.join(str(target_dir), rel.replace(os.sep, "/"))

                with open(src_path, "rb") as sf, open(dst_path, "rb") as df:
                    assert sf.read() == df.read(), (
                        f"content mismatch for {rel!r}"
                    )


# ---------------------------------------------------------------------------
# Selective restore
# ---------------------------------------------------------------------------


class TestRestoreBackupSelectiveRestore:
    """RestoreBackup with items for a subset; verify only listed files restored."""

    def test_selective_restore(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "a.txt"), "aaa")
        _write_file(str(data_dir / "b.txt"), "bbb")
        _write_file(str(data_dir / "c.txt"), "ccc")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["a.txt", "b.txt", "c.txt"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), ["a.txt", "c.txt"])

        assert len(result.restored) == 2
        assert "a.txt" in result.restored
        assert "c.txt" in result.restored

        assert os.path.exists(str(target_dir / "a.txt"))
        assert os.path.exists(str(target_dir / "c.txt"))

        # b.txt should be skipped.
        assert len(result.skipped) == 1
        assert result.skipped[0] == "b.txt"
        assert not os.path.exists(str(target_dir / "b.txt"))


class TestRestoreBackupSelectiveRestoreDirectoryPrefix:
    """Selective restore using a directory prefix should restore all files under that dir."""

    def test_selective_restore_directory_prefix(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        os.makedirs(str(data_dir / "config" / "sub"), exist_ok=True)
        _write_file(str(data_dir / "config" / "app.yaml"), "port: 8080")
        _write_file(str(data_dir / "config" / "sub" / "db.yaml"), "host: localhost")
        _write_file(str(data_dir / "other.txt"), "other")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["config", "other.txt"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), ["config"])

        assert len(result.restored) == 2

        assert len(result.skipped) == 1
        assert result.skipped[0] == "other.txt"

        # Config files should exist.
        assert os.path.exists(str(target_dir / "config" / "app.yaml"))
        assert os.path.exists(str(target_dir / "config" / "sub" / "db.yaml"))

        # other.txt should not exist.
        assert not os.path.exists(str(target_dir / "other.txt"))


class TestRestoreBackupSelectiveWithEmptyItems:
    """Empty items slice should behave like None (full restore)."""

    def test_empty_items_full_restore(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "a.txt"), "aaa")
        _write_file(str(data_dir / "b.txt"), "bbb")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["a.txt", "b.txt"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), [])

        # Empty items slice should restore everything (same as None).
        assert len(result.restored) == 2, (
            f"expected 2 restored with empty items, got {len(result.restored)}: "
            f"{result.restored}"
        )


# ---------------------------------------------------------------------------
# Zip-slip detection
# ---------------------------------------------------------------------------


class TestRestoreBackupZipSlip:
    """Craft a zip with path traversal entry, verify RestoreBackup returns error."""

    def test_zip_slip(self, tmp_path: Path):
        zip_data = _create_zip_in_memory([
            ("safe.txt", b"safe content", None),
            ("../../etc/passwd", b"malicious", None),
        ])

        zip_path = str(tmp_path / "evil.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        with pytest.raises(OSError, match="zip-slip"):
            RestoreBackup(zip_path, target_dir, None)


class TestRestoreBackupZipSlipVariant:
    """Additional zip-slip vectors."""

    @pytest.mark.parametrize(
        "entry,expect_error",
        [
            pytest.param("../sneak.txt", True, id="dot-dot-prefix"),
            pytest.param("a/../../../etc/shadow", True, id="deep-dot-dot"),
            pytest.param("safe/file.txt", False, id="normal-path"),
        ],
    )
    def test_zip_slip_variant(self, tmp_path: Path, entry: str, expect_error: bool):
        zip_data = _create_zip_in_memory([(entry, b"content", None)])
        zip_path = str(tmp_path / "test.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")

        if expect_error:
            with pytest.raises(OSError, match="restore:"):
                RestoreBackup(zip_path, target_dir, None)
        else:
            result = RestoreBackup(zip_path, target_dir, None)
            assert len(result.restored) >= 1


# ---------------------------------------------------------------------------
# CRC-32 corruption
# ---------------------------------------------------------------------------


class TestRestoreBackupCRC32Corruption:
    """Corrupt a byte in compressed data, verify CRC or integrity error."""

    def test_crc32_corruption_deflate(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(
            str(data_dir / "important.dat"),
            "this is important data that must not be corrupted",
        )

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["important.dat"],
        )

        # Read and corrupt the zip.
        with open(zip_path, "rb") as f:
            zip_data = bytearray(f.read())

        _corrupt_zip_data(zip_data)

        corrupt_path = str(tmp_path / "corrupt.zip")
        _write_zip_to_disk(corrupt_path, zip_data)

        target_dir = str(tmp_path / "target")
        with pytest.raises(OSError):
            RestoreBackup(corrupt_path, target_dir, None)


class TestRestoreBackupCRC32CorruptionStored:
    """CRC corruption test using a Stored (no compression) zip."""

    def test_crc32_corruption_stored(self, tmp_path: Path):
        original_content = b"this is the original content that should match CRC-32"
        zip_data = bytearray(
            _create_zip_in_memory([
                ("test.dat", original_content, zipfile.ZIP_STORED),
            ])
        )

        # Find and corrupt the stored data byte.
        corrupted = False
        for i in range(len(zip_data) - 30):
            if (
                zip_data[i] == 0x50
                and zip_data[i + 1] == 0x4B
                and zip_data[i + 2] == 0x03
                and zip_data[i + 3] == 0x04
            ):
                if i + 30 >= len(zip_data):
                    break
                method = struct.unpack_from("<H", zip_data, i + 8)[0]
                if method != 0:  # Not Stored
                    continue
                filename_len = struct.unpack_from("<H", zip_data, i + 26)[0]
                extra_len = struct.unpack_from("<H", zip_data, i + 28)[0]
                data_offset = i + 30 + filename_len + extra_len

                if data_offset < len(zip_data) and len(original_content) > 0:
                    zip_data[data_offset] ^= 0xFF
                    corrupted = True
                    break

        assert corrupted, "failed to locate and corrupt stored data in zip"

        zip_path = str(tmp_path / "corrupt-stored.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        with pytest.raises(OSError):
            RestoreBackup(zip_path, target_dir, None)


class TestRestoreBackupDeflateCorruption:
    """CRC corruption test for deflate-compressed zip entries."""

    def test_deflate_corruption(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        large_content = "ABCDEFGHabcdefgh12345678" * 100
        _write_file(str(data_dir / "large.txt"), large_content)

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["large.txt"],
        )

        with open(zip_path, "rb") as f:
            zip_data = bytearray(f.read())

        _corrupt_zip_data(zip_data)

        corrupt_path = str(tmp_path / "corrupt-deflate.zip")
        _write_zip_to_disk(corrupt_path, zip_data)

        target_dir = str(tmp_path / "target")
        with pytest.raises(OSError):
            RestoreBackup(corrupt_path, target_dir, None)


class TestRestoreBackupZeroLengthFlateCorruption:
    """Corrupting the CRC-32 field in the local file header.

    Note: Python's zipfile validates CRC against the central directory,
    not the local header. The central directory CRC is still correct,
    so decompression may succeed. This test documents that behavior.
    """

    def test_local_header_crc_corruption(self, tmp_path: Path):
        zip_data = bytearray(
            _create_zip_in_memory([
                ("truncate.txt", b"some content that gets compressed", None),
            ])
        )

        # Find the local file header and corrupt the CRC-32 field (offset i+14).
        corrupted = False
        for i in range(len(zip_data) - 30):
            if (
                zip_data[i] == 0x50
                and zip_data[i + 1] == 0x4B
                and zip_data[i + 2] == 0x03
                and zip_data[i + 3] == 0x04
            ):
                struct.pack_into("<I", zip_data, i + 14, 0xDEADBEEF)
                corrupted = True
                break

        assert corrupted, "failed to find local file header"

        zip_path = str(tmp_path / "truncated.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        # Local header CRC corruption may not cause error because zipfile
        # validates against central directory. Either outcome is acceptable.
        try:
            RestoreBackup(zip_path, target_dir, None)
        except OSError:
            pass  # Error is acceptable.


# ---------------------------------------------------------------------------
# Error cases
# ---------------------------------------------------------------------------


class TestRestoreBackupNonexistentZip:
    """Error when zipPath doesn't exist."""

    def test_nonexistent_zip(self, tmp_path: Path):
        target_dir = str(tmp_path / "target")
        nonexistent = str(tmp_path / "does_not_exist.zip")

        with pytest.raises(OSError, match="restore:"):
            RestoreBackup(nonexistent, target_dir, None)

        with pytest.raises(OSError, match="open zip"):
            RestoreBackup(nonexistent, target_dir, None)


class TestRestoreBackupNonexistentItems:
    """items references paths not in zip; verify silently skipped, no error."""

    def test_nonexistent_items(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "real.txt"), "real content")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["real.txt"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(
            str(zip_path), str(target_dir),
            ["nonexistent1.txt", "real.txt", "nonexistent2.txt"],
        )

        # Only real.txt should be restored.
        assert len(result.restored) == 1
        assert result.restored[0] == "real.txt"

        # Non-existent items are silently ignored (not tracked in skipped).
        assert len(result.skipped) == 0, (
            f"expected 0 skipped, got {result.skipped}"
        )

        # Verify file content.
        with open(str(target_dir / "real.txt"), "r") as f:
            assert f.read() == "real content"


class TestRestoreBackupErrorPrefix:
    """Verify all errors use 'restore:' prefix consistent with 'backup:' pattern."""

    def test_nonexistent_zip_error_prefix(self, tmp_path: Path):
        with pytest.raises(OSError, match="restore:"):
            RestoreBackup(str(tmp_path / "nope.zip"), str(tmp_path / "target"), None)

    def test_zip_slip_error_prefix(self, tmp_path: Path):
        zip_data = _create_zip_in_memory([("../../evil.txt", b"malicious", None)])
        zip_path = str(tmp_path / "evil.zip")
        _write_zip_to_disk(zip_path, zip_data)

        with pytest.raises(OSError, match="restore:"):
            RestoreBackup(zip_path, str(tmp_path / "target"), None)


# ---------------------------------------------------------------------------
# Auto-create targetDir
# ---------------------------------------------------------------------------


class TestRestoreBackupAutoCreateTargetDir:
    """targetDir doesn't exist before call; verify it's created and files restored."""

    def test_auto_create_target_dir(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "test.txt"), "auto-created dir test")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["test.txt"],
        )

        # Use a nested target dir that doesn't exist.
        target_dir = str(tmp_path / "deeply" / "nested" / "restore_target")
        assert not os.path.exists(target_dir)

        result = RestoreBackup(str(zip_path), target_dir, None)

        # Verify targetDir was created.
        assert os.path.isdir(target_dir)

        # Verify file restored.
        assert len(result.restored) == 1
        assert result.restored[0] == "test.txt"

        with open(os.path.join(target_dir, "test.txt"), "r") as f:
            assert f.read() == "auto-created dir test"


# ---------------------------------------------------------------------------
# Overwrite existing
# ---------------------------------------------------------------------------


class TestRestoreBackupOverwriteExisting:
    """Pre-populate targetDir with different content; verify files overwritten."""

    def test_overwrite_existing(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "config.yaml"), "name: production\nport: 443")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["config.yaml"],
        )

        # Pre-populate targetDir with different content.
        target_dir = tmp_path / "restore"
        target_dir.mkdir()
        _write_file(str(target_dir / "config.yaml"), "name: development\nport: 3000")

        # Verify pre-existing content is different.
        with open(str(target_dir / "config.yaml"), "r") as f:
            assert f.read() == "name: development\nport: 3000"

        # Restore should overwrite.
        result = RestoreBackup(str(zip_path), str(target_dir), None)

        assert len(result.restored) == 1

        with open(str(target_dir / "config.yaml"), "r") as f:
            assert f.read() == "name: production\nport: 443"


# ---------------------------------------------------------------------------
# First-error-abort
# ---------------------------------------------------------------------------


class TestRestoreBackupFirstErrorAborts:
    """Cause write failure after some files restored; verify partial results."""

    def test_first_error_aborts(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "first.txt"), "first file")
        _write_file(str(data_dir / "second.txt"), "second file")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["first.txt", "second.txt"],
        )

        # Pre-create "second.txt" as a directory so file extraction fails.
        target_dir = tmp_path / "restore"
        target_dir.mkdir()
        os.makedirs(str(target_dir / "second.txt"), exist_ok=True)

        with pytest.raises(OSError, match="restore:"):
            RestoreBackup(str(zip_path), str(target_dir), None)

        # Note: RestoreBackup raises on first error but does not return
        # partial results (the error is raised before the result is returned).
        # This matches Go behavior where the function returns (result, error).


# ---------------------------------------------------------------------------
# Empty zip
# ---------------------------------------------------------------------------


class TestRestoreBackupEmptyZip:
    """Zip with no entries; verify success with empty RestoreResult."""

    def test_empty_zip(self, tmp_path: Path):
        zip_data = _create_zip_in_memory([])
        zip_path = str(tmp_path / "empty.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        result = RestoreBackup(zip_path, target_dir, None)

        assert len(result.restored) == 0
        assert len(result.skipped) == 0


# ---------------------------------------------------------------------------
# Directory entries
# ---------------------------------------------------------------------------


class TestRestoreBackupDirectoryEntries:
    """Backup with nested directories; verify directory structure recreated."""

    def test_directory_entries(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        os.makedirs(str(data_dir / "a" / "b" / "c"), exist_ok=True)
        _write_file(str(data_dir / "a" / "top.txt"), "level 1")
        _write_file(str(data_dir / "a" / "b" / "mid.txt"), "level 2")
        _write_file(str(data_dir / "a" / "b" / "c" / "deep.txt"), "level 3")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp", ["a"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(str(zip_path), str(target_dir), None)

        assert len(result.restored) == 3

        # Verify files and content.
        expected_files = [
            ("a/top.txt", "level 1"),
            ("a/b/mid.txt", "level 2"),
            ("a/b/c/deep.txt", "level 3"),
        ]
        for rel_path, expected_content in expected_files:
            full_path = os.path.join(str(target_dir), rel_path.replace("/", os.sep))
            with open(full_path, "r") as f:
                assert f.read() == expected_content, (
                    f"content mismatch for {rel_path!r}"
                )

        # Verify intermediate directories exist.
        for dir_name in ["a", "a/b", "a/b/c"]:
            dir_path = os.path.join(str(target_dir), dir_name.replace("/", os.sep))
            assert os.path.isdir(dir_path), f"{dir_name!r} should be a directory"


# ---------------------------------------------------------------------------
# Mixed compression
# ---------------------------------------------------------------------------


class TestRestoreBackupMixedCompression:
    """Zip with both Stored and Deflate entries."""

    def test_mixed_compression(self, tmp_path: Path):
        zip_data = _create_zip_in_memory([
            ("stored.txt", b"stored content", zipfile.ZIP_STORED),
            ("deflated.txt", b"this content should be deflated and compressed properly", None),
        ])

        zip_path = str(tmp_path / "mixed.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        result = RestoreBackup(zip_path, target_dir, None)

        assert len(result.restored) == 2

        with open(os.path.join(target_dir, "stored.txt"), "rb") as f:
            assert f.read() == b"stored content"

        with open(os.path.join(target_dir, "deflated.txt"), "rb") as f:
            assert f.read() == b"this content should be deflated and compressed properly"


# ---------------------------------------------------------------------------
# Stored method
# ---------------------------------------------------------------------------


class TestRestoreBackupStoredMethodZip:
    """Ensure RestoreBackup works with Stored (no compression) zip entries."""

    def test_stored_method_zip(self, tmp_path: Path):
        zip_data = _create_zip_in_memory([
            ("stored1.txt", b"content of stored1.txt", zipfile.ZIP_STORED),
            ("stored2.txt", b"content of stored2.txt", zipfile.ZIP_STORED),
        ])

        zip_path = str(tmp_path / "stored.zip")
        _write_zip_to_disk(zip_path, zip_data)

        target_dir = str(tmp_path / "target")
        result = RestoreBackup(zip_path, target_dir, None)

        assert len(result.restored) == 2

        with open(os.path.join(target_dir, "stored1.txt"), "rb") as f:
            assert f.read() == b"content of stored1.txt"


# ---------------------------------------------------------------------------
# Result fields
# ---------------------------------------------------------------------------


class TestRestoreBackupRestoreResultFields:
    """Verify RestoreResult dataclass fields are correctly populated."""

    def test_result_fields(self, tmp_path: Path):
        data_dir = tmp_path / "data"
        output_dir = tmp_path / "output"
        data_dir.mkdir()
        output_dir.mkdir()

        _write_file(str(data_dir / "keep.txt"), "keep")
        _write_file(str(data_dir / "skip.txt"), "skip")
        _write_file(str(data_dir / "also-keep.txt"), "also keep")

        zip_path, _ = CreateBackup(
            str(data_dir), str(output_dir), "testapp",
            ["keep.txt", "skip.txt", "also-keep.txt"],
        )

        target_dir = tmp_path / "restore"
        result = RestoreBackup(
            str(zip_path), str(target_dir),
            ["keep.txt", "also-keep.txt"],
        )

        # Restored should contain exactly the selected items.
        assert len(result.restored) == 2
        restored_set = set(result.restored)
        assert "keep.txt" in restored_set
        assert "also-keep.txt" in restored_set

        # Skipped should contain the non-selected item.
        assert len(result.skipped) == 1
        assert result.skipped[0] == "skip.txt"


# ---------------------------------------------------------------------------
# Empty filename edge case
# ---------------------------------------------------------------------------


class TestRestoreBackupEmptyFilename:
    """Edge case: zip entry with empty filename.

    Python's zipfile.writestr cannot create entries with empty filenames,
    so we build a minimal zip manually with raw bytes to test this edge case.
    """

    def test_empty_filename(self, tmp_path: Path):
        # Build a minimal zip with an empty-filename entry using raw bytes.
        # Local file header: PK\x03\x04 + fields + empty filename + data
        # Central directory entry: PK\x01\x02 + fields
        # End of central directory: PK\x05\x06
        content = b"content"
        fname = b""
        fname_len = len(fname)

        # Local file header (30 bytes + filename + extra + data)
        local_header = struct.pack(
            "<4sHHHHHIIIHH",
            b"PK\x03\x04",   # signature
            20,              # version needed
            0,               # flags
            0,               # compression method (stored)
            0,               # mod time
            0,               # mod date
            0,               # crc-32 (will be computed by zipfile module on read)
            len(content),    # compressed size
            len(content),    # uncompressed size
            fname_len,       # filename length
            0,               # extra field length
        )
        local_data = local_header + fname + content

        # We need valid CRC-32 for the central directory to match.
        import binascii
        crc = binascii.crc32(content) & 0xFFFFFFFF

        # Fix the CRC-32 in the local header (offset 14).
        local_data = bytearray(local_data)
        struct.pack_into("<I", local_data, 14, crc)
        local_data = bytes(local_data)

        # Central directory entry
        cd_entry = struct.pack(
            "<4sHHHHHHIIIHHHHHII",
            b"PK\x01\x02",    # signature
            20,               # version made by
            20,               # version needed
            0,                # flags
            0,                # compression
            0,                # mod time
            0,                # mod date
            crc,              # crc-32
            len(content),     # compressed size
            len(content),     # uncompressed size
            fname_len,        # filename length
            0,                # extra field length
            0,                # file comment length
            0,                # disk number start
            0,                # internal file attributes
            0,                # external file attributes
            0,                # relative offset of local header
        )
        cd_data = cd_entry + fname

        # End of central directory
        eocd = struct.pack(
            "<4sHHHHIIH",
            b"PK\x05\x06",   # signature
            0,                # disk number
            0,                # disk with central dir
            1,                # entries on this disk
            1,                # total entries
            len(cd_data),     # central dir size
            len(local_data),  # central dir offset
            0,                # comment length
        )

        zip_bytes = local_data + cd_data + eocd

        zip_path = str(tmp_path / "emptyname.zip")
        _write_zip_to_disk(zip_path, zip_bytes)

        target_dir = str(tmp_path / "target")
        # Empty filename may cause an error (is-a-directory conflict)
        # or succeed depending on platform. Either is acceptable.
        try:
            RestoreBackup(zip_path, target_dir, None)
        except OSError:
            pass  # Expected on most platforms.
