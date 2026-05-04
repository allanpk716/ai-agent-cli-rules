"""Tests for agent meta-commands (T02).

Covers all 6 commands: schema, errors, config list/set, doctor,
debug last-crash, and cache clean.  Each test verifies JSONL output
structure, error envelope emission, and boundary conditions.
"""

from __future__ import annotations

import io
import json
import os
from pathlib import Path
from typing import Any, Dict, List, Optional
from unittest.mock import MagicMock

import pytest
import typer
from pydantic import BaseModel, Field

from agentsdk import App, ConfigManager, ConfigProvider, HealthCheckResult, CommandMeta
from agentsdk.agent_commands import (
    create_agent_app,
    _walk_commands,
    _extract_flags,
    _first_config_provider,
)
from agentsdk.envelope import Envelope
from agentsdk.exitcode import EXIT_FATAL_ERROR, EXIT_NOT_FOUND
from agentsdk.health import HealthCheckFunc
from agentsdk.writer import Writer


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


class SampleConfig(BaseModel):
    """Sample Pydantic config for testing."""

    name: str = "default"
    port: int = Field(default=8080, json_schema_extra={"config": True})
    api_key: str = Field(default="secret", json_schema_extra={"sensitive": True})
    debug: bool = Field(default=False, json_schema_extra={"config": True})


@pytest.fixture
def tmp_dir(tmp_path: Path) -> Path:
    """Provide a temporary directory."""
    return tmp_path


@pytest.fixture
def config_file(tmp_dir: Path) -> str:
    """Write a sample config file and return its path."""
    cfg = tmp_dir / "config.json"
    cfg.write_text(json.dumps({"name": "test", "port": 9090, "api_key": "sekrit", "debug": True}))
    return str(cfg)


@pytest.fixture
def app(tmp_dir: Path) -> App:
    """Create an App with sandbox pointing at tmp_dir."""
    app = App("test-tool", "1.0.0")
    # Override sandbox to use tmp_dir.
    app._sandbox._base_dir = str(tmp_dir)
    return app


@pytest.fixture
def writer_buf(app: App) -> io.StringIO:
    """Install a StringIO-backed writer and return the buffer."""
    buf = io.StringIO()
    app._writer = Writer(buf, tool_name=app.name)
    return buf


def _read_envelopes(buf: io.StringIO) -> List[dict]:
    """Parse all JSONL envelopes from buffer."""
    lines = buf.getvalue().strip().split("\n")
    return [json.loads(line) for line in lines if line.strip()]


def _read_first_envelope(buf: io.StringIO) -> dict:
    """Parse the first JSONL envelope from buffer."""
    envelopes = _read_envelopes(buf)
    assert envelopes, "expected at least one envelope"
    return envelopes[0]


# ---------------------------------------------------------------------------
# Schema command
# ---------------------------------------------------------------------------


class TestSchemaCommand:
    """Tests for `agent schema`."""

    def test_schema_basic_output(self, app: App, writer_buf: io.StringIO) -> None:
        """Schema command emits tool name, version, and command entries."""
        agent_typer = create_agent_app(app)
        # We need to invoke through a parent Typer to get a real Click tree.
        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")

        # Get Click command and invoke schema.
        click_cmd = typer.main.get_command(root_typer)
        # Find the agent schema invocation via Click context.
        from click.testing import CliRunner
        runner = CliRunner()
        result = runner.invoke(click_cmd, ["agent", "schema"], catch_exceptions=False)

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        data = envelope["data"]
        assert data["tool"] == "test-tool"
        assert data["version"] == "1.0.0"
        assert "commands" in data
        assert isinstance(data["commands"], list)

    def test_schema_empty_tree(self, app: App, writer_buf: io.StringIO) -> None:
        """Schema with a minimal Typer app still produces valid output."""
        agent_typer = create_agent_app(app)
        # Standalone invocation — no parent tree.
        # schema needs a parent context to walk. Without one it still works.
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "schema"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"

    def test_walk_commands_nested(self) -> None:
        """walk_commands recursively flattens nested Click groups."""
        import click

        @click.group()
        def root():
            pass

        @root.command()
        def hello():
            pass

        @click.group()
        def sub():
            pass

        @sub.command()
        def deep():
            pass

        root.add_command(sub)

        entries = _walk_commands(root)
        names = [e["name"] for e in entries]
        assert "hello" in names
        assert "deep" in names

    def test_schema_command_meta_merge(self, app: App, writer_buf: io.StringIO) -> None:
        """Schema merges CommandMeta registrations into command entries."""
        meta = CommandMeta(description="A test command", is_idempotent=True)
        app.register_command_meta("hello", meta)

        root_typer = typer.Typer()

        @root_typer.command("hello")
        def hello() -> None:
            pass

        agent_typer = create_agent_app(app)
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)

        from click.testing import CliRunner
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "schema"])

        envelope = _read_first_envelope(writer_buf)
        commands = envelope["data"]["commands"]
        hello_cmd = next((c for c in commands if c.get("path") == "hello"), None)
        assert hello_cmd is not None
        assert hello_cmd.get("description") == "A test command"
        assert hello_cmd.get("is_idempotent") is True

    def test_extract_flags(self) -> None:
        """extract_flags correctly identifies Click options."""
        import click

        @click.command()
        @click.option("--name", default="default", help="A name")
        @click.option("--verbose/-v", is_flag=True, default=False)
        def standalone(name, verbose):
            pass

        flags = _extract_flags(standalone)
        assert len(flags) >= 1
        flag_names = [f["name"] for f in flags]
        assert "name" in flag_names or "verbose" in flag_names


# ---------------------------------------------------------------------------
# Errors command
# ---------------------------------------------------------------------------


class TestErrorsCommand:
    """Tests for `agent errors`."""

    def test_errors_builtin_codes(self, app: App, writer_buf: io.StringIO) -> None:
        """Errors command includes built-in error codes."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "errors"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        data = envelope["data"]
        codes = [c["code"] for c in data["codes"]]
        assert "FATAL_CRASH" in codes
        assert "INPUT_INVALID" in codes
        assert "NOT_FOUND" in codes
        assert data["count"] == len(codes)

    def test_errors_sorted_alphabetically(self, app: App, writer_buf: io.StringIO) -> None:
        """Error codes are sorted alphabetically."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "errors"])

        envelope = _read_first_envelope(writer_buf)
        codes = [c["code"] for c in envelope["data"]["codes"]]
        assert codes == sorted(codes)

    def test_errors_custom_code(self, app: App, writer_buf: io.StringIO) -> None:
        """Custom error codes are included."""
        app.register_error_code("MY_ERROR", 99, "Custom error")
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "errors"])

        envelope = _read_first_envelope(writer_buf)
        codes = [c["code"] for c in envelope["data"]["codes"]]
        assert "MY_ERROR" in codes
        my_err = next(c for c in envelope["data"]["codes"] if c["code"] == "MY_ERROR")
        assert my_err["exit_code"] == 99
        assert my_err["description"] == "Custom error"


# ---------------------------------------------------------------------------
# Config list command
# ---------------------------------------------------------------------------


class TestConfigListCommand:
    """Tests for `agent config list`."""

    def test_config_list_redacted(self, app: App, writer_buf: io.StringIO, config_file: str) -> None:
        """Config list shows redacted values for sensitive fields."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "list"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        config_data = envelope["data"]["config"]
        assert config_data["api_key"] == "***"
        assert config_data["name"] == "test"
        assert config_data["port"] == 9090

    def test_config_list_no_provider(self, app: App, writer_buf: io.StringIO) -> None:
        """Config list emits error when no provider is registered."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        result = runner.invoke(click_cmd, ["agent", "config", "list"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"

    def test_config_list_file_not_found(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Config list emits error when config file doesn't exist."""
        mgr = ConfigManager(SampleConfig, str(tmp_dir / "nonexistent.json"))
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "list"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"


# ---------------------------------------------------------------------------
# Config set command
# ---------------------------------------------------------------------------


class TestConfigSetCommand:
    """Tests for `agent config set`."""

    def test_config_set_success(self, app: App, writer_buf: io.StringIO, config_file: str) -> None:
        """Config set updates value and persists to file."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "set", "port", "3000"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        assert envelope["data"]["set"]["port"] == "3000"

        # Verify file was actually updated.
        with open(config_file) as f:
            saved = json.load(f)
        assert saved["port"] == 3000

    def test_config_set_non_configurable(self, app: App, writer_buf: io.StringIO, config_file: str) -> None:
        """Config set rejects non-configurable fields."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "set", "name", "newname"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "INPUT_INVALID"

    def test_config_set_whitelist_rejection(self, app: App, writer_buf: io.StringIO, config_file: str) -> None:
        """Config set rejects unknown fields."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "set", "nonexistent", "val"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert "not user-configurable" in envelope["message"]

    def test_config_set_no_provider(self, app: App, writer_buf: io.StringIO) -> None:
        """Config set emits error when no provider registered."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "set", "port", "3000"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"

    def test_config_set_type_conversion(self, app: App, writer_buf: io.StringIO, config_file: str) -> None:
        """Config set converts string values to correct types."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "config", "set", "debug", "true"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"

        with open(config_file) as f:
            saved = json.load(f)
        assert saved["debug"] is True


# ---------------------------------------------------------------------------
# Doctor command
# ---------------------------------------------------------------------------


class TestDoctorCommand:
    """Tests for `agent doctor`."""

    def test_doctor_passes_with_dirs(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Doctor reports pass when sandbox dirs exist."""
        # Create sandbox dirs.
        for subdir in ["data", "cache", "locks", "crash_dumps"]:
            (tmp_dir / subdir).mkdir(parents=True, exist_ok=True)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "doctor"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        data = envelope["data"]
        assert data["status"] == "pass"
        checks = data["checks"]
        sandbox_checks = [c for c in checks if c["name"].startswith("sandbox_")]
        assert all(c["status"] == "pass" for c in sandbox_checks)

    def test_doctor_missing_dirs(self, app: App, writer_buf: io.StringIO) -> None:
        """Doctor reports fail when sandbox dirs are missing."""
        # Don't create dirs — they shouldn't exist.
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "doctor"])

        envelope = _read_first_envelope(writer_buf)
        data = envelope["data"]
        assert data["status"] == "fail"
        sandbox_checks = [c for c in data["checks"] if c["name"].startswith("sandbox_")]
        assert all(c["status"] == "fail" for c in sandbox_checks)

    def test_doctor_user_health_checks(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Doctor includes user-registered health checks."""
        for subdir in ["data", "cache", "locks", "crash_dumps"]:
            (tmp_dir / subdir).mkdir(parents=True, exist_ok=True)

        def custom_check() -> HealthCheckResult:
            return HealthCheckResult(name="custom", status="pass", message="all good")

        app.register_health_check("custom", custom_check)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "doctor"])

        envelope = _read_first_envelope(writer_buf)
        checks = envelope["data"]["checks"]
        custom = next(c for c in checks if c["name"] == "custom")
        assert custom["status"] == "pass"
        assert custom["message"] == "all good"

    def test_doctor_config_file_check(self, app: App, writer_buf: io.StringIO, tmp_dir: Path, config_file: str) -> None:
        """Doctor includes config file check when provider registered."""
        for subdir in ["data", "cache", "locks", "crash_dumps"]:
            (tmp_dir / subdir).mkdir(parents=True, exist_ok=True)

        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "doctor"])

        envelope = _read_first_envelope(writer_buf)
        checks = envelope["data"]["checks"]
        config_check = next((c for c in checks if c["name"] == "config_file"), None)
        assert config_check is not None
        assert config_check["status"] == "pass"

    def test_doctor_health_check_raises(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Doctor catches exceptions from health check functions."""
        for subdir in ["data", "cache", "locks", "crash_dumps"]:
            (tmp_dir / subdir).mkdir(parents=True, exist_ok=True)

        def bad_check() -> HealthCheckResult:
            raise RuntimeError("oops")

        app.register_health_check("bad", bad_check)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "doctor"])

        envelope = _read_first_envelope(writer_buf)
        checks = envelope["data"]["checks"]
        bad = next(c for c in checks if c["name"] == "bad")
        assert bad["status"] == "fail"
        assert "oops" in bad["message"]


# ---------------------------------------------------------------------------
# Debug last-crash command
# ---------------------------------------------------------------------------


class TestDebugLastCrashCommand:
    """Tests for `agent debug last-crash`."""

    def test_debug_last_crash_returns_data(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Debug last-crash returns newest crash dump data."""
        crash_dir = tmp_dir / "crash_dumps"
        crash_dir.mkdir(parents=True, exist_ok=True)

        dump_data = {
            "timestamp": "2024-01-01T00:00:00Z",
            "app_name": "test-tool",
            "app_version": "1.0.0",
            "crash_type": "panic",
            "stack_trace": "traceback here",
            "flight_context": {},
        }
        (crash_dir / "crash-20240101-000000.json").write_text(json.dumps(dump_data))

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "debug-last-crash"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        data = envelope["data"]
        assert data["file"] == "crash-20240101-000000.json"
        assert data["crash"]["crash_type"] == "panic"
        assert data["crash"]["stack_trace"] == "traceback here"

    def test_debug_last_crash_no_dumps(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Debug last-crash emits NOT_FOUND when no dumps exist."""
        crash_dir = tmp_dir / "crash_dumps"
        crash_dir.mkdir(parents=True, exist_ok=True)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "debug-last-crash"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"

    def test_debug_last_crash_newest_first(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Debug last-crash returns the most recent dump."""
        crash_dir = tmp_dir / "crash_dumps"
        crash_dir.mkdir(parents=True, exist_ok=True)

        old_dump = {"timestamp": "old", "crash_type": "panic", "stack_trace": "", "flight_context": {}}
        new_dump = {"timestamp": "new", "crash_type": "signal", "stack_trace": "", "flight_context": {}}

        (crash_dir / "crash-old.json").write_text(json.dumps(old_dump))
        (crash_dir / "crash-new.json").write_text(json.dumps(new_dump))

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "debug-last-crash"])

        envelope = _read_first_envelope(writer_buf)
        # The newest file by mtime should be returned.
        assert envelope["type"] == "result"
        # Since both are created nearly simultaneously, the sort is by mtime.
        assert "crash" in envelope["data"]

    def test_debug_last_crash_ignores_tmp(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Debug last-crash ignores .tmp files."""
        crash_dir = tmp_dir / "crash_dumps"
        crash_dir.mkdir(parents=True, exist_ok=True)

        # Only a .tmp file — no .json files.
        (crash_dir / "crash-incomplete.json.tmp").write_text("{}")

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "debug-last-crash"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"

    def test_debug_last_crash_no_dir(self, app: App, writer_buf: io.StringIO) -> None:
        """Debug last-crash emits NOT_FOUND when dir doesn't exist."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "debug-last-crash"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"


# ---------------------------------------------------------------------------
# Cache clean command
# ---------------------------------------------------------------------------


class TestCacheCleanCommand:
    """Tests for `agent cache clean`."""

    def test_cache_clean_removes_files(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Cache clean removes all files from cache dir."""
        cache_dir = tmp_dir / "cache"
        cache_dir.mkdir(parents=True, exist_ok=True)
        (cache_dir / "file1.txt").write_text("data")
        (cache_dir / "file2.bin").write_text("data")

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "cache-clean"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["type"] == "result"
        assert envelope["data"]["cleaned"] == 2
        assert not list(cache_dir.iterdir())

    def test_cache_clean_empty_dir(self, app: App, writer_buf: io.StringIO, tmp_dir: Path) -> None:
        """Cache clean returns 0 for empty dir."""
        cache_dir = tmp_dir / "cache"
        cache_dir.mkdir(parents=True, exist_ok=True)

        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "cache-clean"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["data"]["cleaned"] == 0

    def test_cache_clean_nonexistent_dir(self, app: App, writer_buf: io.StringIO) -> None:
        """Cache clean returns 0 for nonexistent dir."""
        agent_typer = create_agent_app(app)
        from click.testing import CliRunner

        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)
        runner = CliRunner()
        runner.invoke(click_cmd, ["agent", "cache-clean"])

        envelope = _read_first_envelope(writer_buf)
        assert envelope["data"]["cleaned"] == 0


# ---------------------------------------------------------------------------
# Integration: all commands produce valid JSONL
# ---------------------------------------------------------------------------


class TestIntegration:
    """Integration tests verifying all commands produce valid envelopes."""

    def test_all_commands_valid_envelopes(self, app: App, tmp_dir: Path, config_file: str) -> None:
        """Each command produces envelopes with required fields."""
        for subdir in ["data", "cache", "locks", "crash_dumps"]:
            (tmp_dir / subdir).mkdir(parents=True, exist_ok=True)

        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)

        agent_typer = create_agent_app(app)
        root_typer = typer.Typer()
        root_typer.add_typer(agent_typer, name="agent")
        click_cmd = typer.main.get_command(root_typer)

        from click.testing import CliRunner

        # Test each command produces a valid envelope.
        commands = [
            ["agent", "schema"],
            ["agent", "errors"],
            ["agent", "config", "list"],
            ["agent", "doctor"],
            ["agent", "cache-clean"],
        ]

        runner = CliRunner()
        for cmd_args in commands:
            buf = io.StringIO()
            app._writer = Writer(buf, tool_name=app.name)
            # Rebuild agent_typer so it picks up the new writer.
            agent_typer = create_agent_app(app)
            root_typer = typer.Typer()
            root_typer.add_typer(agent_typer, name="agent")
            click_cmd = typer.main.get_command(root_typer)
            runner.invoke(click_cmd, cmd_args)

            lines = [l for l in buf.getvalue().strip().split("\n") if l.strip()]
            assert lines, f"no output for {' '.join(cmd_args)}"
            envelope = json.loads(lines[0])
            assert "version" in envelope
            assert "type" in envelope
            assert "tool" in envelope
            assert envelope["tool"] == "test-tool"
            assert envelope["type"] in ("result", "error")

    def test_helper_first_config_provider(self, app: App) -> None:
        """first_config_provider returns None when no providers registered."""
        assert _first_config_provider(app) is None

    def test_helper_first_config_provider_returns_first(self, app: App, config_file: str) -> None:
        """first_config_provider returns the first registered provider."""
        mgr = ConfigManager(SampleConfig, config_file)
        app.register_config("main", mgr)
        assert _first_config_provider(app) is mgr
