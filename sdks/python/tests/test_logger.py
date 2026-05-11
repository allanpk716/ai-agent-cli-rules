"""Tests for agentsdk.logger — Logger, WithFieldFormatter, LoggerSettings."""

from __future__ import annotations

import logging
import os
import re
import tempfile
from pathlib import Path

import pytest

from agentsdk.logger import (
    Logger,
    LoggerSettings,
    WithFieldFormatter,
    _BoundLogger,
    default_logger_settings,
)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

# Matches: YYYY-MM-DD HH:MM:SS.mmm - [LEVEL]: ...
_LOG_LINE_RE = re.compile(
    r"^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} - \[\w+\]:"
)
_LEVEL_RE = re.compile(r"\[(DEBUG|INFO|WARNING|ERROR|CRITICAL)\]")


def _create_test_logger(tmp_path, name="testapp", level=logging.INFO):
    """Create a Logger writing to tmp_path with test defaults."""
    settings = default_logger_settings(name, str(tmp_path))
    settings.level = level
    settings.rotation_time_hours = 24
    settings.max_age_days = 1
    logger = Logger(settings)
    return logger


def _find_log_file(directory):
    """Return the path of the first non-directory file in *directory*."""
    for entry in os.listdir(directory):
        full = os.path.join(directory, entry)
        if os.path.isfile(full):
            return full
    return None


def _read_log_lines(directory):
    """Read all non-empty lines from the first log file found."""
    path = _find_log_file(directory)
    if path is None:
        return []
    with open(path, encoding="utf-8") as f:
        lines = [l.rstrip("\n") for l in f if l.strip()]
    return lines


# ---------------------------------------------------------------------------
# Settings tests
# ---------------------------------------------------------------------------


class TestDefaultLoggerSettings:
    def test_defaults(self):
        s = default_logger_settings("myapp", "/tmp/logs")
        assert s.level == logging.INFO
        assert s.log_dir == "/tmp/logs"
        assert s.log_name_base == "myapp"
        assert s.rotation_time_hours == 24
        assert s.max_age_days == 7
        assert s.max_size_mb == 0


# ---------------------------------------------------------------------------
# Format tests
# ---------------------------------------------------------------------------


class TestWithFieldFormatter:
    def test_format_no_fields(self):
        fmt = WithFieldFormatter()
        record = logging.LogRecord(
            name="test", level=logging.INFO, pathname="x.py", lineno=1,
            msg="hello world", args=(), exc_info=None,
        )
        output = fmt.format(record)
        assert _LOG_LINE_RE.match(output), f"bad format: {output!r}"
        assert "[INFO]" in output
        assert "hello world" in output

    def test_format_with_fields(self):
        fmt = WithFieldFormatter()
        record = logging.LogRecord(
            name="test", level=logging.INFO, pathname="x.py", lineno=1,
            msg="login", args=(), exc_info=None,
        )
        record.bound_fields = {"user_id": 123}
        output = fmt.format(record)
        assert "user_id=123" in output

    def test_format_with_multiple_fields(self):
        fmt = WithFieldFormatter()
        record = logging.LogRecord(
            name="test", level=logging.INFO, pathname="x.py", lineno=1,
            msg="test", args=(), exc_info=None,
        )
        record.bound_fields = {"a": 1, "b": "two"}
        output = fmt.format(record)
        assert "a=1" in output
        assert "b=two" in output

    def test_format_empty_fields(self):
        fmt = WithFieldFormatter()
        record = logging.LogRecord(
            name="test", level=logging.INFO, pathname="x.py", lineno=1,
            msg="msg", args=(), exc_info=None,
        )
        record.bound_fields = {}
        output = fmt.format(record)
        # Empty fields dict should not add trailing space
        assert output.endswith("msg")

    def test_format_no_fields_attribute(self):
        fmt = WithFieldFormatter()
        record = logging.LogRecord(
            name="test", level=logging.INFO, pathname="x.py", lineno=1,
            msg="plain", args=(), exc_info=None,
        )
        output = fmt.format(record)
        assert output.endswith("plain")


# ---------------------------------------------------------------------------
# Logger integration tests
# ---------------------------------------------------------------------------


class TestLoggerLevels:
    @pytest.mark.parametrize(
        "level,method,want",
        [
            (logging.DEBUG, "debug", "DEBUG"),
            (logging.INFO, "info", "INFO"),
            (logging.WARNING, "warn", "WARNING"),
            (logging.ERROR, "error", "ERROR"),
        ],
    )
    def test_all_levels(self, tmp_path, level, method, want):
        logger = _create_test_logger(tmp_path, level=level)
        getattr(logger, method)("test msg")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        matches = _LEVEL_RE.search(lines[-1])
        assert matches, f"no level in: {lines[-1]!r}"
        assert matches.group(1) == want
        logger.close()


class TestLoggerFormat:
    def test_info_format(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.info("hello world")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert _LOG_LINE_RE.match(line), f"bad format: {line!r}"
        assert "[INFO]" in line
        assert "hello world" in line
        logger.close()


class TestLoggerWithField:
    def test_with_field(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_field("user_id", 123).info("login")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert "user_id=123" in line
        assert "login" in line
        logger.close()

    def test_with_fields(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_fields({"a": 1, "b": "two"}).info("test")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert "a=1" in line
        assert "b=two" in line
        assert "test" in line
        logger.close()

    def test_with_field_chain(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_field("count", 42).with_field("ratio", 3.14).info("multi")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert "count=42" in line
        assert "ratio=3.14" in line
        logger.close()

    def test_with_fields_none(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_fields(None).info("nil fields")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert "nil fields" in line
        logger.close()

    def test_with_fields_empty(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_fields({}).info("empty fields")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        logger.close()

    def test_with_field_complex_values(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_field("path", "/some/path with spaces/file.txt").info("complex")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        assert "path=/some/path" in line
        logger.close()


class TestLoggerFormatContract:
    """Verify exact format: YYYY-MM-DD HH:MM:SS.mmm - [LEVEL]: msg key=value"""

    def test_exact_format(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.with_field("key", 123).info("hello")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        line = lines[-1]
        ts = r"\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3}"
        pattern = rf"^{ts} - \[INFO\]: hello key=123$"
        assert re.match(pattern, line), (
            f"log line does not match format contract\n"
            f"  got:      {line!r}\n"
            f"  expected: {pattern}"
        )
        logger.close()


class TestLoggerDegradation:
    def test_unwritable_dir_falls_back_to_stderr(self, tmp_path):
        # Use a file path as the "directory" — makedirs on a file will fail.
        blocker = tmp_path / "afile"
        blocker.write_text("block")
        settings = default_logger_settings("degtest", str(blocker))
        logger = Logger(settings)
        # Should not raise — just prints to stderr.
        logger.info("this goes to stderr only")
        logger.close()

    def test_close_stderr_only(self, tmp_path):
        blocker = tmp_path / "blocker"
        blocker.write_text("x")
        settings = default_logger_settings("stdtest", str(blocker))
        logger = Logger(settings)
        result = logger.close()
        assert result is None  # safe no-op


class TestLoggerClose:
    def test_close(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.info("before close")
        result = logger.close()
        assert result is None
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1

    def test_close_idempotent(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        assert logger.close() is None
        assert logger.close() is None  # second call is safe


class TestLoggerIndependence:
    def test_multiple_instances(self, tmp_path):
        dir1 = tmp_path / "dir1"
        dir2 = tmp_path / "dir2"
        dir1.mkdir()
        dir2.mkdir()

        logger1 = _create_test_logger(dir1, name="app1")
        logger2 = _create_test_logger(dir2, name="app2")

        logger1.info("from_app1")
        logger2.info("from_app2")

        lines1 = _read_log_lines(dir1)
        lines2 = _read_log_lines(dir2)

        assert len(lines1) >= 1
        assert len(lines2) >= 1

        # Verify isolation.
        for line in lines1:
            assert "from_app2" not in line
        for line in lines2:
            assert "from_app1" not in line

        logger1.close()
        logger2.close()


class TestLoggerRotation:
    def test_file_created(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.info("rotation test")
        logger.close()
        log_file = _find_log_file(tmp_path)
        assert log_file is not None, "expected at least one log file"

    def test_size_based_rotation(self, tmp_path):
        settings = default_logger_settings("sizetest", str(tmp_path))
        settings.max_size_mb = 1
        settings.max_age_days = 1
        logger = Logger(settings)
        logger.info("size rotation test")
        logger.close()
        entries = os.listdir(tmp_path)
        found = any(e.endswith(".log") for e in entries if os.path.isfile(os.path.join(tmp_path, e)))
        assert found, "expected .log file with size-based rotation"


class TestLoggerEdgeCases:
    def test_empty_message(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        logger.info("")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        assert _LOG_LINE_RE.match(lines[-1])
        logger.close()

    def test_special_characters(self, tmp_path):
        logger = _create_test_logger(tmp_path)
        msg = "special: \t\"quotes\" and 'apostrophes' & <tags>"
        logger.info(msg)
        # Read full file since message may have newlines.
        path = _find_log_file(tmp_path)
        assert path is not None
        with open(path, encoding="utf-8") as f:
            content = f.read()
        assert "special:" in content
        assert "quotes" in content
        assert "apostrophes" in content
        logger.close()

    def test_debug_level_below_info(self, tmp_path):
        """DEBUG messages are captured when level is DEBUG."""
        logger = _create_test_logger(tmp_path, level=logging.DEBUG)
        logger.debug("dbg msg")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        assert "[DEBUG]" in lines[-1]
        logger.close()

    def test_debug_suppressed_at_info_level(self, tmp_path):
        """DEBUG messages are suppressed when level is INFO."""
        logger = _create_test_logger(tmp_path, level=logging.INFO)
        logger.debug("should not appear")
        logger.info("should appear")
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
        assert "should appear" in lines[-1]
        # The debug message should not be in the file.
        content = "\n".join(lines)
        assert "should not appear" not in content
        logger.close()


class TestLoggerSettingsDataclass:
    def test_custom_settings(self):
        s = LoggerSettings(
            level=logging.DEBUG,
            log_dir="/var/log/app",
            log_name_base="myapp",
            rotation_time_hours=12,
            max_age_days=30,
            max_size_mb=10,
        )
        assert s.level == logging.DEBUG
        assert s.log_dir == "/var/log/app"
        assert s.log_name_base == "myapp"
        assert s.rotation_time_hours == 12
        assert s.max_age_days == 30
        assert s.max_size_mb == 10


class TestExpiredLogCleanup:
    def test_cleanup_removes_old_files(self, tmp_path):
        """Files with mtime older than max_age_days are deleted on init."""
        # Create an old log file.
        old_file = tmp_path / "old-app--20200101.log"
        old_file.write_text("old log\n")
        # Set mtime to 30 days ago.
        import time as _time
        old_time = _time.time() - (31 * 86400)
        os.utime(str(old_file), (old_time, old_time))

        # Create logger with max_age_days=7 — should clean up old file.
        settings = default_logger_settings("cleanup", str(tmp_path))
        settings.max_age_days = 7
        logger = Logger(settings)
        logger.info("new log")
        logger.close()

        assert not old_file.exists(), "old log file should have been cleaned up"
        # New log should still exist.
        lines = _read_log_lines(tmp_path)
        assert len(lines) >= 1
