"""SignalHandler — graceful shutdown on OS signals.

Registers handlers for ``SIGINT`` (all platforms) and ``SIGTERM`` (POSIX
only). On signal receipt, captures a FlightContext snapshot, writes a crash
dump file, and invokes the configured exit callback.

Uses injectable :class:`SignalHandlerConfig` for testability — tests can
override ``sys.exit()`` and error handling without side effects.
"""

from __future__ import annotations

import signal
import sys
import traceback
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Callable, Dict, List, Optional

from agentsdk.crashdump import CrashDump, write_crash_dump
from agentsdk.flightcontext import FlightContext
from agentsdk.sandbox import Sandbox


@dataclass
class SignalHandlerConfig:
    """Configuration for :func:`setup_signal_handler`.

    Attributes:
        on_signal: Called after the crash dump is written. Defaults to
            ``sys.exit(1)``.
        on_write_error: Called when the crash dump write fails. Defaults to
            printing to stderr.
    """

    on_signal: Optional[Callable[[], None]] = None
    on_write_error: Optional[Callable[[Exception], None]] = None


def _default_on_signal() -> None:
    """Default signal callback — exit with code 1."""
    sys.exit(1)


def _default_on_write_error(err: Exception) -> None:
    """Default write-error callback — print to stderr."""
    print(f"crashdump: signal handler write failed: {err}", file=sys.stderr)


def setup_signal_handler(
    app_name: str,
    app_version: str,
    trace_id: str,
    sandbox: Sandbox,
    flight_context: FlightContext,
    config: Optional[SignalHandlerConfig] = None,
) -> Callable[[], None]:
    """Install signal handlers for SIGINT and (on POSIX) SIGTERM.

    On signal receipt:
        1. Capture a stack trace via ``traceback.format_stack()``.
        2. Build a :class:`CrashDump` and write it atomically.
        3. Call the ``on_signal`` callback (defaults to ``sys.exit(1)``).

    Args:
        app_name: Application name for the crash dump.
        app_version: Application version for the crash dump.
        trace_id: Distributed trace ID active at crash time.
        sandbox: Sandbox providing the crash_dumps directory.
        flight_context: In-flight state to snapshot on crash.
        config: Optional configuration overrides for testing.

    Returns:
        A stop function that restores default signal handlers. Safe to call
        multiple times.
    """
    cfg = config or SignalHandlerConfig()
    on_signal = cfg.on_signal or _default_on_signal
    on_write_error = cfg.on_write_error or _default_on_write_error

    # Track original handlers so the stop function can restore them.
    _originals: List[tuple] = []  # (sig, original_handler)

    def _handler(sig: int, frame: Any) -> None:
        """Signal handler callback."""
        stack_trace = "".join(traceback.format_stack())
        sig_name = _signal_name(sig)

        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name=app_name,
            app_version=app_version,
            trace_id=trace_id,
            crash_type="signal",
            signal=sig_name,
            stack_trace=stack_trace,
            flight_context=flight_context.snapshot(),
        )

        try:
            write_crash_dump(sandbox, dump)
        except Exception as err:
            on_write_error(err)

        on_signal()

    # Register SIGINT on all platforms.
    _originals.append((signal.SIGINT, signal.signal(signal.SIGINT, _handler)))

    # Register SIGTERM only on POSIX (not available on Windows).
    if hasattr(signal, "SIGTERM"):
        _originals.append((signal.SIGTERM, signal.signal(signal.SIGTERM, _handler)))  # type: ignore[attr-defined]

    def stop() -> None:
        """Restore original signal handlers.

        Safe to call multiple times — subsequent calls are no-ops.
        """
        for sig, orig in _originals:
            signal.signal(sig, orig)
        _originals.clear()

    return stop


def _signal_name(sig: int) -> str:
    """Return a human-readable signal name."""
    if sig == signal.SIGINT:
        return "SIGINT"
    if hasattr(signal, "SIGTERM") and sig == signal.SIGTERM:  # type: ignore[attr-defined]
        return "SIGTERM"
    # Fallback for unknown signals.
    try:
        return signal.Signals(sig).name
    except (ValueError, AttributeError):
        return f"SIG{sig}"
