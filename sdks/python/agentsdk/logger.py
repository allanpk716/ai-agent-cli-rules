"""Logger — structured logging with WithField support.

Provides a Logger class that wraps Python's stdlib logging module with a
WithFieldFormatter producing output identical to the Go SDK's format::

    YYYY-MM-DD HH:MM:SS.mmm - [LEVEL]: message key=value

Each Logger instance is fully independent — no global state is shared.
Multiple Loggers can coexist without interference. If the log directory
cannot be created or the file handler fails, the Logger degrades
gracefully to stderr-only output (no exception is raised).
"""

from __future__ import annotations

import logging
import os
import sys
import time
from dataclasses import dataclass
from logging.handlers import RotatingFileHandler, TimedRotatingFileHandler
from typing import Any, Dict, Optional


# ---------------------------------------------------------------------------
# LoggerSettings
# ---------------------------------------------------------------------------


@dataclass
class LoggerSettings:
    """Configuration for a Logger instance.

    Attributes:
        level: Minimum log level (default: ``logging.INFO``).
        log_dir: Directory where log files are written.
        log_name_base: Base name for log files (without extension).
        rotation_time_hours: Hours between time-based rotations (default: 24).
        max_age_days: Maximum days to retain old log files (default: 7).
        max_size_mb: Enable size-based rotation when > 0 (default: 0).
    """

    level: int = logging.INFO
    log_dir: str = ""
    log_name_base: str = ""
    rotation_time_hours: int = 24
    max_age_days: int = 7
    max_size_mb: int = 0


def default_logger_settings(app_name: str, log_dir: str) -> LoggerSettings:
    """Return a LoggerSettings with sensible defaults.

    Args:
        app_name: Used as the base name for log files.
        log_dir: Directory where log files are written.

    Returns:
        A new LoggerSettings instance.
    """
    return LoggerSettings(
        level=logging.INFO,
        log_dir=log_dir,
        log_name_base=app_name,
        rotation_time_hours=24,
        max_age_days=7,
        max_size_mb=0,
    )


# ---------------------------------------------------------------------------
# WithFieldFormatter
# ---------------------------------------------------------------------------


class WithFieldFormatter(logging.Formatter):
    """Formatter producing output identical to Go SDK's WithFieldFormatter.

    Output format::

        YYYY-MM-DD HH:MM:SS.mmm - [LEVEL]: message key=value

    Fields attached via ``Logger.with_field`` / ``Logger.with_fields`` are
    appended as space-separated ``key=value`` pairs after the message.
    """

    # Sentinel key used to pass bound fields through LogRecord.extra.
    _FIELDS_ATTR = "bound_fields"

    def format(self, record: logging.LogRecord) -> str:
        # Build timestamp: YYYY-MM-DD HH:MM:SS.mmm
        ct = self.converter(record.created)
        ts = time.strftime("%Y-%m-%d %H:%M:%S", ct)
        msecs = int(record.msecs)
        timestamp = f"{ts}.{msecs:03d}"

        # Level and message
        level = record.levelname
        msg = record.getMessage()

        parts = [f"{timestamp} - [{level}]: {msg}"]

        # Append bound fields if present
        fields: Optional[Dict[str, Any]] = getattr(
            record, self._FIELDS_ATTR, None
        )
        if fields:
            field_strs = []
            for k, v in fields.items():
                field_strs.append(f"{k}={v}")
            if field_strs:
                parts.append(" ".join(field_strs))

        line = " ".join(parts)

        # Handle exception info (matches stdlib Formatter behaviour)
        if record.exc_info and not record.exc_text:
            record.exc_text = self.formatException(record.exc_info)
        if record.exc_text:
            if line[-1:] != "\n":
                line += "\n"
            line += record.exc_text

        return line


# ---------------------------------------------------------------------------
# _BoundLogger
# ---------------------------------------------------------------------------


class _BoundLogger:
    """A logger with pre-attached fields.

    Returned by :meth:`Logger.with_field` and :meth:`Logger.with_fields`.
    All log methods delegate to the parent :class:`Logger` while injecting
    the accumulated fields into the log record.
    """

    def __init__(self, logger: Logger, fields: Dict[str, Any]) -> None:
        self._logger = logger
        self._fields: Dict[str, Any] = dict(fields)

    # -- Log methods --------------------------------------------------------

    def debug(self, msg: str) -> None:
        self._log(logging.DEBUG, msg)

    def info(self, msg: str) -> None:
        self._log(logging.INFO, msg)

    def warn(self, msg: str) -> None:
        self._log(logging.WARNING, msg)

    def error(self, msg: str) -> None:
        self._log(logging.ERROR, msg)

    def _log(self, level: int, msg: str) -> None:
        self._logger._log_impl(level, msg, self._fields)

    # -- Field builders -----------------------------------------------------

    def with_field(self, key: str, value: Any) -> _BoundLogger:
        """Return a new bound logger with an additional field."""
        new_fields = dict(self._fields)
        new_fields[key] = value
        return _BoundLogger(self._logger, new_fields)

    def with_fields(self, fields: Optional[Dict[str, Any]]) -> _BoundLogger:
        """Return a new bound logger with additional fields merged in."""
        new_fields = dict(self._fields)
        if fields:
            new_fields.update(fields)
        return _BoundLogger(self._logger, new_fields)


# ---------------------------------------------------------------------------
# Logger
# ---------------------------------------------------------------------------


class Logger:
    """Independent logging instance wrapping stdlib logging with file rotation.

    Each Logger has its own ``logging.Logger``, file handler, and settings.
    There is no global state — multiple Loggers can coexist without
    interference.

    Usage::

        settings = default_logger_settings("myapp", "/var/log/myapp")
        logger = Logger(settings)
        logger.with_field("user_id", 123).info("login")
        logger.close()
    """

    def __init__(self, settings: LoggerSettings) -> None:
        self._settings = settings
        self._closed = False
        self._file_handler: Optional[logging.Handler] = None
        self._stderr_handler: Optional[logging.StreamHandler] = None
        self._stderr_only = False

        # Create an independent logger (not the root logger).
        self._internal_logger = logging.getLogger(f"agentsdk.{id(self)}")
        self._internal_logger.setLevel(settings.level)
        self._internal_logger.propagate = False

        # Attempt to create log directory.
        try:
            os.makedirs(settings.log_dir, exist_ok=True)
        except OSError:
            print(
                f"logger: failed to create log dir {settings.log_dir!r}"
                " — falling back to stderr only",
                file=sys.stderr,
            )
            self._stderr_only = True

        # Create formatter.
        formatter = WithFieldFormatter()

        # Set up stderr handler (always present).
        self._stderr_handler = logging.StreamHandler(sys.stderr)
        self._stderr_handler.setFormatter(formatter)
        self._internal_logger.addHandler(self._stderr_handler)

        # Set up file handler if directory is usable.
        if not self._stderr_only:
            log_file = os.path.join(
                settings.log_dir, f"{settings.log_name_base}.log"
            )
            try:
                if settings.max_size_mb > 0:
                    handler = RotatingFileHandler(
                        log_file,
                        maxBytes=settings.max_size_mb * 1024 * 1024,
                        backupCount=0,
                    )
                else:
                    handler = TimedRotatingFileHandler(
                        log_file,
                        when="H",
                        interval=settings.rotation_time_hours,
                        backupCount=0,
                    )
                handler.setFormatter(formatter)
                self._file_handler = handler
                self._internal_logger.addHandler(handler)
            except (OSError, ValueError):
                print(
                    "logger: failed to create file handler"
                    " — falling back to stderr only",
                    file=sys.stderr,
                )
                self._stderr_only = True

        # Best-effort cleanup of expired logs.
        self._cleanup_expired_logs()

    # -- Log methods --------------------------------------------------------

    def debug(self, msg: str) -> None:
        """Log a message at DEBUG level."""
        self._log_impl(logging.DEBUG, msg)

    def info(self, msg: str) -> None:
        """Log a message at INFO level."""
        self._log_impl(logging.INFO, msg)

    def warn(self, msg: str) -> None:
        """Log a message at WARNING level."""
        self._log_impl(logging.WARNING, msg)

    def error(self, msg: str) -> None:
        """Log a message at ERROR level."""
        self._log_impl(logging.ERROR, msg)

    def _log_impl(
        self, level: int, msg: str, fields: Optional[Dict[str, Any]] = None
    ) -> None:
        extra: Dict[str, Any] = {}
        if fields:
            extra[WithFieldFormatter._FIELDS_ATTR] = fields
        self._internal_logger.log(level, msg, extra=extra)

    # -- Field builders -----------------------------------------------------

    def with_field(self, key: str, value: Any) -> _BoundLogger:
        """Return a bound logger with a single attached field."""
        return _BoundLogger(self, {key: value})

    def with_fields(self, fields: Optional[Dict[str, Any]]) -> _BoundLogger:
        """Return a bound logger with multiple attached fields."""
        return _BoundLogger(self, dict(fields) if fields else {})

    # -- Lifecycle ----------------------------------------------------------

    def close(self) -> Optional[str]:
        """Release all handler resources.

        Idempotent — calling close on an already-closed or stderr-only
        Logger is a safe no-op.

        Returns:
            An error description string if any handler failed to close,
            or ``None`` on success.
        """
        if self._closed:
            return None
        self._closed = True

        error: Optional[str] = None
        for handler in list(self._internal_logger.handlers):
            try:
                handler.close()
            except Exception as exc:
                error = str(exc)
            self._internal_logger.removeHandler(handler)

        self._file_handler = None
        self._stderr_handler = None
        return error

    # -- Internal helpers ---------------------------------------------------

    def _cleanup_expired_logs(self) -> None:
        """Best-effort deletion of log files older than max_age_days."""
        max_age = self._settings.max_age_days
        if max_age <= 0:
            return

        log_dir = self._settings.log_dir
        if not log_dir or not os.path.isdir(log_dir):
            return

        cutoff = time.time() - (max_age * 86400)
        try:
            for entry in os.listdir(log_dir):
                path = os.path.join(log_dir, entry)
                if os.path.isfile(path):
                    try:
                        if os.path.getmtime(path) < cutoff:
                            os.remove(path)
                    except OSError:
                        pass
        except OSError:
            pass
