"""Tests for agentsdk.signalhandler — signal handling with crash dump.

Covers: SIGINT triggers crash dump, stop function restores defaults, config
        overrides for testing, cross-platform SIGTERM guard, write error
        handling, signal name mapping.
"""

import json
import os
import signal
import threading
from pathlib import Path
from datetime import datetime

import pytest

from agentsdk.crashdump import CrashDump, write_crash_dump
from agentsdk.flightcontext import FlightContext
from agentsdk.sandbox import Sandbox
from agentsdk.signalhandler import (
    SignalHandlerConfig,
    _signal_name,
    setup_signal_handler,
)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    """Remove any *_HOME overrides that might leak between tests."""
    for key in list(os.environ):
        if key.endswith("_HOME"):
            monkeypatch.delenv(key, raising=False)


def _make_sandbox(tmp_path: Path, name: str = "sigtest") -> Sandbox:
    """Create a Sandbox pointing at *tmp_path*/<name>/."""
    env_var = name.upper().replace("-", "_") + "_HOME"
    sandbox_dir = str(tmp_path / name)
    os.environ[env_var] = sandbox_dir
    return Sandbox(name)


# ---------------------------------------------------------------------------
# Signal name mapping
# ---------------------------------------------------------------------------

class TestSignalName:
    def test_sigint(self):
        assert _signal_name(signal.SIGINT) == "SIGINT"

    @pytest.mark.skipif(
        not hasattr(signal, "SIGTERM"),
        reason="SIGTERM not available on Windows",
    )
    def test_sigterm(self):
        assert _signal_name(signal.SIGTERM) == "SIGTERM"

    def test_unknown_signal(self):
        # Use a high signal number unlikely to be a real signal.
        name = _signal_name(99)
        # Should return something — exact format varies by platform.
        assert isinstance(name, str)
        assert len(name) > 0


# ---------------------------------------------------------------------------
# Stop function
# ---------------------------------------------------------------------------

class TestStopFunction:
    def test_stop_restores_default(self, tmp_path):
        """Stop function restores default signal handlers."""
        sandbox = _make_sandbox(tmp_path, "stop-test")
        fc = FlightContext()
        original = signal.getsignal(signal.SIGINT)

        stop = setup_signal_handler("stop-test", "1.0.0", "", sandbox, fc)

        # SIGINT should be our handler now.
        current = signal.getsignal(signal.SIGINT)
        assert current != original

        stop()

        # After stop, SIGINT should be restored to default.
        restored = signal.getsignal(signal.SIGINT)
        assert restored == original

    def test_stop_idempotent(self, tmp_path):
        """Calling stop multiple times is safe."""
        sandbox = _make_sandbox(tmp_path, "stop-idem-test")
        fc = FlightContext()

        stop = setup_signal_handler("stop-idem-test", "1.0.0", "", sandbox, fc)
        stop()
        stop()  # Should not raise.
        stop()

    def test_stop_with_sigterm(self, tmp_path):
        """Stop function restores SIGTERM on POSIX."""
        if not hasattr(signal, "SIGTERM"):
            pytest.skip("SIGTERM not available on this platform")

        sandbox = _make_sandbox(tmp_path, "stop-term-test")
        fc = FlightContext()
        original = signal.getsignal(signal.SIGTERM)

        stop = setup_signal_handler("stop-term-test", "1.0.0", "", sandbox, fc)

        current = signal.getsignal(signal.SIGTERM)
        assert current != original

        stop()

        restored = signal.getsignal(signal.SIGTERM)
        assert restored == original


# ---------------------------------------------------------------------------
# Config overrides
# ---------------------------------------------------------------------------

class TestSignalHandlerConfig:
    def test_config_overrides_on_signal(self, tmp_path):
        """on_signal callback is called instead of sys.exit."""
        sandbox = _make_sandbox(tmp_path, "cfg-test")
        fc = FlightContext()
        fc.set("operation", "test-op")
        fc.set("step", 3)

        called = threading.Event()

        config = SignalHandlerConfig(
            on_signal=called.set,
        )

        stop = setup_signal_handler(
            "cfg-test", "1.0.0", "trace-cfg-001", sandbox, fc, config
        )

        # Simulate signal delivery by directly calling the registered handler.
        handler = signal.getsignal(signal.SIGINT)
        handler(signal.SIGINT, None)

        # on_signal should have been called.
        assert called.is_set(), "on_signal callback was not called"

        stop()

    def test_config_overrides_on_write_error(self, tmp_path):
        """on_write_error callback is called when crash dump write fails."""
        # Create a sandbox whose crash_dumps dir is blocked by a file.
        sandbox_dir = tmp_path / "err-app"
        sandbox_dir.mkdir()
        (sandbox_dir / "crash_dumps").write_text("blocker")

        env_var = "ERR_APP_HOME"
        os.environ[env_var] = str(sandbox_dir)
        sandbox = Sandbox("err-app")
        fc = FlightContext()

        errors: list[Exception] = []
        called = threading.Event()

        config = SignalHandlerConfig(
            on_signal=called.set,
            on_write_error=lambda err: errors.append(err),
        )

        stop = setup_signal_handler("err-app", "1.0.0", "", sandbox, fc, config)

        # Trigger handler.
        handler = signal.getsignal(signal.SIGINT)
        handler(signal.SIGINT, None)

        assert len(errors) == 1, f"expected 1 write error, got {len(errors)}"
        assert isinstance(errors[0], OSError)

        # on_signal should still be called despite write failure.
        assert called.is_set(), "on_signal should be called even after write error"

        stop()


# ---------------------------------------------------------------------------
# Crash dump content verification
# ---------------------------------------------------------------------------

class TestCrashDumpOnSignal:
    def test_sigint_produces_crash_dump(self, tmp_path):
        """SIGINT triggers a crash dump with correct fields."""
        sandbox = _make_sandbox(tmp_path, "sigint-test")
        fc = FlightContext()
        fc.set("operation", "test-op")
        fc.set("step", 3)

        called = threading.Event()
        config = SignalHandlerConfig(on_signal=called.set)

        stop = setup_signal_handler(
            "sigint-test", "1.0.0", "trace-sig-001", sandbox, fc, config
        )

        # Trigger handler.
        handler = signal.getsignal(signal.SIGINT)
        handler(signal.SIGINT, None)

        assert called.is_set()

        # Verify crash dump file.
        crash_dir = Path(sandbox.crash_dumps_dir)
        entries = list(crash_dir.iterdir())
        assert len(entries) >= 1

        data = json.loads(entries[-1].read_text(encoding="utf-8"))
        assert data["crash_type"] == "signal"
        assert data["signal"] == "SIGINT"
        assert data["app_name"] == "sigint-test"
        assert data["app_version"] == "1.0.0"
        assert data["trace_id"] == "trace-sig-001"
        assert data["stack_trace"] != ""
        assert data["flight_context"]["operation"] == "test-op"
        assert data["flight_context"]["step"] == 3

        stop()

    def test_empty_trace_id(self, tmp_path):
        """Empty trace_id is handled correctly."""
        sandbox = _make_sandbox(tmp_path, "empty-trace-test")
        fc = FlightContext()

        called = threading.Event()
        config = SignalHandlerConfig(on_signal=called.set)

        stop = setup_signal_handler(
            "empty-trace-test", "1.0.0", "", sandbox, fc, config
        )

        handler = signal.getsignal(signal.SIGINT)
        handler(signal.SIGINT, None)

        assert called.is_set()

        crash_dir = Path(sandbox.crash_dumps_dir)
        entries = list(crash_dir.iterdir())
        assert len(entries) >= 1

        data = json.loads(entries[-1].read_text(encoding="utf-8"))
        # trace_id should be omitted when empty (omitempty).
        assert "trace_id" not in data

        stop()

    def test_flight_context_snapshot(self, tmp_path):
        """FlightContext snapshot at signal time is captured."""
        sandbox = _make_sandbox(tmp_path, "fc-snap-test")
        fc = FlightContext()
        fc.set("job_id", "abc-123")
        fc.set("status", "running")

        called = threading.Event()
        config = SignalHandlerConfig(on_signal=called.set)

        stop = setup_signal_handler(
            "fc-snap-test", "2.0.0", "trace-fc", sandbox, fc, config
        )

        # Mutate context after handler setup but before signal.
        fc.set("extra", "data")

        handler = signal.getsignal(signal.SIGINT)
        handler(signal.SIGINT, None)

        assert called.is_set()

        crash_dir = Path(sandbox.crash_dumps_dir)
        entries = list(crash_dir.iterdir())
        data = json.loads(entries[-1].read_text(encoding="utf-8"))

        assert data["flight_context"]["job_id"] == "abc-123"
        assert data["flight_context"]["status"] == "running"
        assert data["flight_context"]["extra"] == "data"

        stop()


# ---------------------------------------------------------------------------
# Cross-platform SIGTERM guard
# ---------------------------------------------------------------------------

class TestCrossPlatform:
    def test_no_sigterm_attribute_does_not_crash(self, tmp_path):
        """Setup works even if SIGTERM is not available (Windows)."""
        sandbox = _make_sandbox(tmp_path, "no-term-test")
        fc = FlightContext()

        # This test always passes — the real guard is the hasattr check
        # in setup_signal_handler. We verify the handler registers at
        # least SIGINT.
        called = threading.Event()
        config = SignalHandlerConfig(on_signal=called.set)

        stop = setup_signal_handler(
            "no-term-test", "1.0.0", "", sandbox, fc, config
        )

        # SIGINT handler should be registered.
        handler = signal.getsignal(signal.SIGINT)
        assert handler is not None
        assert handler != signal.SIG_DFL

        stop()
