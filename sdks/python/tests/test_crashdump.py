"""Tests for agentsdk.crashdump — CrashDump dataclass and atomic write.

Covers: JSON structure with all fields, omitempty semantics, atomic write
        (no .tmp leftover), filename format, auto-create directory, empty
        and large FlightContext values, error on unwritable directory.
"""

import json
import os
from pathlib import Path
from datetime import datetime

import pytest

from agentsdk.crashdump import CrashDump, write_crash_dump
from agentsdk.sandbox import Sandbox


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    """Remove any *_HOME overrides that might leak between tests."""
    for key in list(os.environ):
        if key.endswith("_HOME"):
            monkeypatch.delenv(key, raising=False)


def _make_sandbox(tmp_path: Path, name: str = "test-app") -> Sandbox:
    """Create a Sandbox pointing at *tmp_path*/<name>/."""
    env_var = name.upper().replace("-", "_") + "_HOME"
    sandbox_dir = str(tmp_path / name)
    os.environ[env_var] = sandbox_dir
    return Sandbox(name)


# ---------------------------------------------------------------------------
# CrashDump dataclass
# ---------------------------------------------------------------------------

class TestCrashDumpDataclass:
    def test_json_structure_all_fields(self):
        """All fields are present in JSON output when populated."""
        dump = CrashDump(
            timestamp="2025-01-15T10:30:00+00:00",
            app_name="test-app",
            app_version="1.2.3",
            trace_id="trace-abc123",
            crash_type="signal",
            signal="SIGTERM",
            stack_trace="goroutine 1 [running]:\nmain.main()",
            flight_context={"agent": "planner", "task_id": "T01"},
        )
        d = dump.to_dict()

        assert d["timestamp"] == "2025-01-15T10:30:00+00:00"
        assert d["app_name"] == "test-app"
        assert d["app_version"] == "1.2.3"
        assert d["trace_id"] == "trace-abc123"
        assert d["crash_type"] == "signal"
        assert d["signal"] == "SIGTERM"
        assert d["stack_trace"] == "goroutine 1 [running]:\nmain.main()"
        assert d["flight_context"]["agent"] == "planner"
        assert d["flight_context"]["task_id"] == "T01"

    def test_omit_empty_trace_id(self):
        """Empty trace_id is excluded from JSON (omitempty)."""
        dump = CrashDump(
            timestamp="2025-01-15T10:30:00+00:00",
            app_name="test-app",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGINT",
            stack_trace="stack",
            trace_id="",
        )
        d = dump.to_dict()
        assert "trace_id" not in d

    def test_omit_empty_signal(self):
        """Empty signal is excluded from JSON (omitempty)."""
        dump = CrashDump(
            timestamp="2025-01-15T10:30:00+00:00",
            app_name="test-app",
            app_version="1.0.0",
            crash_type="panic",
            panic_value="runtime error: index out of range",
            stack_trace="stack",
            signal="",
        )
        d = dump.to_dict()
        assert "signal" not in d
        assert d["panic_value"] == "runtime error: index out of range"

    def test_omit_empty_panic_value(self):
        """Empty panic_value is excluded from JSON (omitempty)."""
        dump = CrashDump(
            timestamp="2025-01-15T10:30:00+00:00",
            app_name="test-app",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGTERM",
            stack_trace="stack",
            panic_value="",
        )
        d = dump.to_dict()
        assert "panic_value" not in d

    def test_panic_type_omits_signal_in_json(self):
        """Panic-type crash dump should omit signal field."""
        dump = CrashDump(
            timestamp="2025-01-15T10:30:00+00:00",
            app_name="test-app",
            app_version="1.0.0",
            crash_type="panic",
            panic_value="runtime error: index out of range",
            stack_trace="stack",
            signal="",
            flight_context={},
        )
        serialized = json.dumps(dump.to_dict())
        assert "signal" not in serialized
        assert "panic_value" in serialized

    def test_default_flight_context(self):
        """FlightContext defaults to empty dict."""
        dump = CrashDump(
            timestamp="now",
            app_name="app",
            app_version="1.0",
            crash_type="signal",
            stack_trace="stack",
        )
        assert dump.flight_context == {}
        assert dump.to_dict()["flight_context"] == {}


# ---------------------------------------------------------------------------
# write_crash_dump
# ---------------------------------------------------------------------------

class TestWriteCrashDump:
    def test_creates_file(self, tmp_path):
        """WriteCrashDump creates a file in crash_dumps/."""
        sandbox = _make_sandbox(tmp_path, "crashtest")
        dump = CrashDump(
            timestamp="2025-06-01T12:00:00+00:00",
            app_name="crashtest",
            app_version="0.1.0",
            trace_id="trace-001",
            crash_type="signal",
            signal="SIGINT",
            stack_trace="fake stack",
            flight_context={"step": "processing"},
        )

        result = write_crash_dump(sandbox, dump)

        crash_dir = Path(sandbox.crash_dumps_dir)
        entries = list(crash_dir.iterdir())
        assert len(entries) == 1
        assert entries[0] == Path(result)

        # Verify contents parse back.
        data = json.loads(entries[0].read_text(encoding="utf-8"))
        assert data["signal"] == "SIGINT"
        assert data["flight_context"]["step"] == "processing"

    def test_atomic_write_no_tmp_leftover(self, tmp_path):
        """No .tmp file remains after a successful write."""
        sandbox = _make_sandbox(tmp_path, "atomic-test")
        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="atomic-test",
            app_version="1.0.0",
            crash_type="panic",
            panic_value="test panic",
            stack_trace="stack",
            flight_context={},
        )

        write_crash_dump(sandbox, dump)

        crash_dir = Path(sandbox.crash_dumps_dir)
        for entry in crash_dir.iterdir():
            assert not entry.name.endswith(".tmp"), f"leftover .tmp file: {entry.name}"

    def test_creates_missing_directory(self, tmp_path):
        """WriteCrashDump creates crash_dumps/ dir if it doesn't exist."""
        sandbox = _make_sandbox(tmp_path, "mkdir-test")
        crash_dir = Path(sandbox.crash_dumps_dir)

        # Verify directory doesn't exist yet.
        assert not crash_dir.exists()

        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="mkdir-test",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGTERM",
            stack_trace="stack trace here",
            flight_context={},
        )

        write_crash_dump(sandbox, dump)

        # Directory should now exist with a file in it.
        assert crash_dir.exists()
        entries = list(crash_dir.iterdir())
        assert len(entries) == 1

    def test_filename_format(self, tmp_path):
        """Filename matches crash-YYYYMMDD-HHMMSS.json."""
        sandbox = _make_sandbox(tmp_path, "fnname-test")
        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="fnname-test",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGTERM",
            stack_trace="stack",
            flight_context={},
        )

        write_crash_dump(sandbox, dump)

        entries = list(Path(sandbox.crash_dumps_dir).iterdir())
        assert len(entries) == 1
        name = entries[0].name
        assert name.startswith("crash-")
        assert name.endswith(".json")
        # Length: crash-YYYYMMDD-HHMMSS.json = 26 chars
        assert len(name) == 26

    def test_empty_flight_context(self, tmp_path):
        """Empty FlightContext is preserved as empty dict in JSON."""
        sandbox = _make_sandbox(tmp_path, "empty-fc-test")
        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="empty-fc-test",
            app_version="1.0.0",
            crash_type="panic",
            panic_value="nil pointer",
            stack_trace="stack",
            flight_context={},
        )

        write_crash_dump(sandbox, dump)

        entries = list(Path(sandbox.crash_dumps_dir).iterdir())
        data = json.loads(entries[0].read_text(encoding="utf-8"))
        assert data["flight_context"] == {}

    def test_large_context_values(self, tmp_path):
        """Large and nested values in FlightContext survive round-trip."""
        sandbox = _make_sandbox(tmp_path, "large-ctx-test")
        large_data = ["x" * 100] * 1000
        nested = {"level1": {"level2": "deep value"}}
        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="large-ctx-test",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGTERM",
            stack_trace="stack",
            flight_context={"large_data": large_data, "nested": nested},
        )

        write_crash_dump(sandbox, dump)

        entries = list(Path(sandbox.crash_dumps_dir).iterdir())
        data = json.loads(entries[0].read_text(encoding="utf-8"))

        assert len(data["flight_context"]["large_data"]) == 1000
        assert data["flight_context"]["nested"]["level1"]["level2"] == "deep value"

    def test_error_on_unwritable_dir(self, tmp_path):
        """WriteCrashDump raises an error when the dir is blocked by a file."""
        # Create a file where crash_dumps/ directory should go.
        blocker = tmp_path / "blocked-app"
        blocker.mkdir()
        (blocker / "crash_dumps").write_text("i am a file, not a directory")

        env_var = "BLOCKED_APP_HOME"
        os.environ[env_var] = str(blocker)
        sandbox = Sandbox("blocked-app")

        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="blocked-app",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGINT",
            stack_trace="stack",
            flight_context={},
        )

        with pytest.raises(OSError):
            write_crash_dump(sandbox, dump)

    def test_returns_path(self, tmp_path):
        """write_crash_dump returns the path of the written file."""
        sandbox = _make_sandbox(tmp_path, "return-test")
        dump = CrashDump(
            timestamp=datetime.now().isoformat(),
            app_name="return-test",
            app_version="1.0.0",
            crash_type="signal",
            signal="SIGINT",
            stack_trace="stack",
            flight_context={},
        )

        path = write_crash_dump(sandbox, dump)

        assert Path(path).exists()
        assert "crash-" in path
        assert path.endswith(".json")
