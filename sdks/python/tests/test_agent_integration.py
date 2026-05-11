"""End-to-end integration tests — all 6 agent commands via App.run().

Exercises every agent command through the full App.run() pipeline:
stdout hijacking, JSONL emission, and exception recovery. Uses a real
Typer CLI tree containing both user commands and agent commands.

T03 verification:
  - All 6 command paths tested through App.run() (not just command functions)
  - stdout hijacking verified — no non-JSONL output in real stdout
  - All envelopes pass validate_envelope()
  - Config set → config list round-trip verified
  - Schema includes both user commands and agent commands
  - Negative tests: non-whitelisted field, no dumps, empty cache
"""

from __future__ import annotations

import io
import json
import time
from pathlib import Path
from typing import Any, Dict, List

import pytest
import typer
from pydantic import BaseModel, Field

from agentsdk import (
    App,
    ConfigManager,
    CommandMeta,
    HealthCheckResult,
    validate_envelope,
)
from agentsdk.exitcode import (
    EXIT_SUCCESS,
    EXIT_FATAL_ERROR,
    EXIT_NOT_FOUND,
)


# ---------------------------------------------------------------------------
# Test config model
# ---------------------------------------------------------------------------


class IntegrationConfig(BaseModel):
    """Pydantic config model for integration tests."""

    name: str = "default"
    port: int = Field(default=8080, json_schema_extra={"config": True})
    api_key: str = Field(default="secret", json_schema_extra={"sensitive": True})
    debug: bool = Field(default=False, json_schema_extra={"config": True})


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _write_config(path: Path, data: Dict[str, Any]) -> None:
    """Write a JSON config file."""
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(data))


def _parse_jsonl(output: str) -> List[dict]:
    """Parse JSONL output into a list of dicts."""
    return [json.loads(line) for line in output.strip().split("\n") if line.strip()]


def _first_envelope(output: str) -> dict:
    """Return the first JSONL envelope from output string."""
    envelopes = _parse_jsonl(output)
    assert envelopes, "expected at least one envelope"
    return envelopes[0]


def _make_sandbox_dirs(base: Path) -> None:
    """Create standard sandbox subdirectories."""
    for subdir in ("data", "cache", "locks", "crash_dumps", "logs"):
        (base / subdir).mkdir(parents=True, exist_ok=True)


def _build_cli(app: App) -> typer.Typer:
    """Build a real CLI tree with user commands + agent commands.

    User commands:
      - deploy [--env ENV]
      - status
      - db migrate [--revision REV]

    Agent commands are added via app.agent_commands().
    """
    cli = typer.Typer(name="test-cli", help="Test CLI app")

    @cli.command("deploy")
    def deploy(env: str = typer.Option("staging", help="Target environment")) -> None:
        """Deploy the application."""
        app.writer.success({"deployed_to": env})

    @cli.command("status")
    def status() -> None:
        """Show application status."""
        app.writer.success({"status": "running", "uptime": 42})

    # db group
    db_app = typer.Typer(name="db", help="Database commands")

    @db_app.command("migrate")
    def db_migrate(
        revision: str = typer.Option("head", help="Target revision"),
    ) -> None:
        """Run database migrations."""
        app.writer.success({"migration": revision})

    cli.add_typer(db_app, name="db")

    # Agent commands
    cli.add_typer(app.agent_commands(), name="agent")

    return cli


def _run_agent_command(
    app: App,
    cli: typer.Typer,
    args: List[str],
) -> tuple[int, str]:
    """Run a CLI command via App.run() and capture JSONL output.

    App.run() writes envelopes to ``app._real_stdout`` (captured at
    construction time).  We inject a StringIO buffer there so the
    envelopes land in our buffer.  The ``sys.stdout`` hijacking inside
    run() captures non-SDK output separately.

    Returns (exit_code, jsonl_output).
    """
    buf = io.StringIO()
    # Redirect the App's real stdout so envelopes go to our buffer.
    old_real = app._real_stdout
    app._real_stdout = buf
    try:
        code = app.run(cli, args)
    finally:
        app._real_stdout = old_real
    return code, buf.getvalue()


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def tmp_sandbox(tmp_path: Path) -> Path:
    """Provide a temporary sandbox directory."""
    return tmp_path


@pytest.fixture
def config_file(tmp_sandbox: Path) -> str:
    """Write a sample config file and return its path."""
    cfg_path = tmp_sandbox / "config.json"
    _write_config(
        cfg_path,
        {"name": "integration-test", "port": 9090, "api_key": "sekrit123", "debug": True},
    )
    return str(cfg_path)


@pytest.fixture
def app(tmp_sandbox: Path) -> App:
    """Create an App with sandbox pointing at tmp_sandbox."""
    app = App("test-tool", "1.0.0")
    app._sandbox._base_dir = str(tmp_sandbox)
    return app


@pytest.fixture
def full_app(app: App, tmp_sandbox: Path, config_file: str) -> tuple[App, typer.Typer]:
    """Create a fully wired App with config, health checks, command meta, and CLI."""
    _make_sandbox_dirs(tmp_sandbox)

    # ConfigManager
    mgr = ConfigManager(IntegrationConfig, config_file)
    app.register_config("main", mgr)

    # Custom error code
    app.register_error_code("DB_CONN_FAILED", 4, "Database connection failed")

    # Health check
    def check_db() -> HealthCheckResult:
        return HealthCheckResult(name="database", status="pass", message="connected")

    app.register_health_check("database", check_db)

    # Command meta for user commands (paths include root Typer name)
    app.register_command_meta(
        "test-cli deploy", CommandMeta(description="Deploy the app", is_idempotent=False)
    )
    app.register_command_meta(
        "test-cli status", CommandMeta(description="Show status", is_idempotent=True)
    )

    # Build CLI
    cli = _build_cli(app)
    return app, cli


# ===========================================================================
# Test: Schema command
# ===========================================================================


class TestSchemaIntegration:
    """End-to-end tests for `agent schema` via App.run()."""

    def test_schema_emits_valid_result_envelope(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Schema command produces a valid result envelope through App.run()."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "schema"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        assert envelope["tool"] == "test-tool"
        validate_envelope(envelope)

    def test_schema_includes_user_and_agent_commands(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Schema output includes both user commands (deploy, status, db migrate)
        and agent commands."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "schema"])

        envelope = _first_envelope(output)
        data = envelope["data"]
        paths = [cmd["path"] for cmd in data["commands"]]

        # User commands present (paths include the root Typer name prefix)
        assert any("deploy" in p for p in paths), f"deploy not in schema paths: {paths}"
        assert any("status" in p for p in paths), f"status not in schema paths: {paths}"
        assert any("migrate" in p for p in paths), f"db migrate not in schema paths: {paths}"

        # Agent commands present
        assert any("schema" in p for p in paths), f"agent schema not in schema paths: {paths}"
        assert any("errors" in p for p in paths), f"agent errors not in schema paths: {paths}"
        assert any("doctor" in p for p in paths), f"agent doctor not in schema paths: {paths}"

    def test_schema_includes_command_meta(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Schema merges CommandMeta into command entries."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "schema"])

        envelope = _first_envelope(output)
        commands = envelope["data"]["commands"]

        deploy_cmd = next(c for c in commands if "deploy" in c.get("path", ""))
        assert deploy_cmd.get("description") == "Deploy the app"
        assert deploy_cmd.get("is_idempotent") is False

        status_cmd = next(c for c in commands if "status" in c.get("path", "") and "cache" not in c.get("path", ""))
        assert status_cmd.get("is_idempotent") is True

    def test_schema_stdout_hijacking(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """No non-JSONL output leaks to the captured output stream."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "schema"])

        # All output lines should be valid JSON
        for line in output.strip().split("\n"):
            if line.strip():
                json.loads(line)  # should not raise

        # Captured output (from _FakeStream) should not contain our JSONL
        # (JSONL goes to the real stdout buffer, not the fake stream)
        # app.captured_output should be empty or not contain envelope data
        captured = app.captured_output
        assert "test-tool" not in captured, (
            f"JSONL leaked into captured output: {captured!r}"
        )


# ===========================================================================
# Test: Errors command
# ===========================================================================


class TestErrorsIntegration:
    """End-to-end tests for `agent errors` via App.run()."""

    def test_errors_emits_valid_result_envelope(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Errors command produces a valid result envelope."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "errors"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        assert envelope["tool"] == "test-tool"
        validate_envelope(envelope)

    def test_errors_includes_builtin_and_custom_codes(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Errors output includes both built-in and custom error codes."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "errors"])

        envelope = _first_envelope(output)
        data = envelope["data"]
        codes = {c["code"]: c for c in data["codes"]}

        # Built-in
        assert "FATAL_CRASH" in codes
        assert "NOT_FOUND" in codes
        assert "INPUT_INVALID" in codes

        # Custom
        assert "DB_CONN_FAILED" in codes
        assert codes["DB_CONN_FAILED"]["exit_code"] == 4
        assert codes["DB_CONN_FAILED"]["description"] == "Database connection failed"

        # Count matches
        assert data["count"] == len(data["codes"])

    def test_errors_sorted_alphabetically(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Error codes are sorted alphabetically."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "errors"])

        envelope = _first_envelope(output)
        code_names = [c["code"] for c in envelope["data"]["codes"]]
        assert code_names == sorted(code_names)


# ===========================================================================
# Test: Config list command
# ===========================================================================


class TestConfigListIntegration:
    """End-to-end tests for `agent config list` via App.run()."""

    def test_config_list_emits_valid_result(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Config list produces a valid result envelope with redacted values."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "config", "list"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        validate_envelope(envelope)

    def test_config_list_redacts_sensitive_fields(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Config list redacts sensitive fields preventing credential exposure."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "config", "list"])

        envelope = _first_envelope(output)
        config_data = envelope["data"]["config"]

        # Sensitive field redacted
        assert config_data["api_key"] == "***", (
            f"api_key should be redacted, got: {config_data['api_key']}"
        )

        # Non-sensitive fields visible
        assert config_data["name"] == "integration-test"
        assert config_data["port"] == 9090
        assert config_data["debug"] is True

    def test_config_list_no_provider(
        self, app: App, tmp_sandbox: Path
    ) -> None:
        """Config list emits error envelope when no provider registered."""
        _make_sandbox_dirs(tmp_sandbox)
        cli = _build_cli(app)

        code, output = _run_agent_command(app, cli, ["agent", "config", "list"])

        # Should get a non-success exit code
        assert code != EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"
        validate_envelope(envelope)


# ===========================================================================
# Test: Config set command
# ===========================================================================


class TestConfigSetIntegration:
    """End-to-end tests for `agent config set` via App.run()."""

    def test_config_set_emits_valid_result(
        self, full_app: tuple[App, typer.Typer], config_file: str
    ) -> None:
        """Config set produces a valid result envelope."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "config", "set", "port", "3000"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        assert envelope["data"]["set"]["port"] == "3000"
        validate_envelope(envelope)

    def test_config_set_persists_to_file(
        self, full_app: tuple[App, typer.Typer], config_file: str
    ) -> None:
        """Config set writes the new value to the config file."""
        app, cli = full_app
        _run_agent_command(app, cli, ["agent", "config", "set", "port", "3000"])

        with open(config_file) as f:
            saved = json.load(f)
        assert saved["port"] == 3000

    def test_config_set_then_list_round_trip(
        self, full_app: tuple[App, typer.Typer], config_file: str
    ) -> None:
        """Config set followed by config list shows the updated value."""
        app, cli = full_app

        # Set port to 3000
        _run_agent_command(app, cli, ["agent", "config", "set", "port", "3000"])

        # List config — should show updated port
        _, output = _run_agent_command(app, cli, ["agent", "config", "list"])
        envelope = _first_envelope(output)
        config_data = envelope["data"]["config"]
        assert config_data["port"] == 3000

    def test_config_set_bool_type_conversion(
        self, full_app: tuple[App, typer.Typer], config_file: str
    ) -> None:
        """Config set converts string 'false' to bool False."""
        app, cli = full_app
        _run_agent_command(app, cli, ["agent", "config", "set", "debug", "false"])

        with open(config_file) as f:
            saved = json.load(f)
        assert saved["debug"] is False

    def test_config_set_non_whitelisted_field_rejected(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Config set rejects non-whitelisted fields with INPUT_INVALID error."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "config", "set", "name", "hacked"])

        assert code != EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "INPUT_INVALID"
        validate_envelope(envelope)

    def test_config_set_unknown_field_rejected(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Config set rejects unknown fields with INPUT_INVALID error."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "config", "set", "totally_fake", "val"])

        assert code != EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "INPUT_INVALID"
        assert "not user-configurable" in envelope["message"]


# ===========================================================================
# Test: Doctor command
# ===========================================================================


class TestDoctorIntegration:
    """End-to-end tests for `agent doctor` via App.run()."""

    def test_doctor_emits_valid_result_with_checks(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Doctor produces a valid result envelope with per-check pass/fail status."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["agent", "doctor"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        validate_envelope(envelope)

        data = envelope["data"]
        assert "status" in data
        assert "checks" in data
        assert isinstance(data["checks"], list)
        assert len(data["checks"]) > 0

    def test_doctor_passes_when_healthy(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Doctor reports overall 'pass' when all checks pass."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "doctor"])

        envelope = _first_envelope(output)
        data = envelope["data"]
        assert data["status"] == "pass"

        # Verify each check has name and status
        for check in data["checks"]:
            assert "name" in check
            assert "status" in check
            assert check["status"] in ("pass", "fail", "warning")

    def test_doctor_includes_sandbox_and_config_checks(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Doctor includes sandbox dir checks and config file check."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "doctor"])

        envelope = _first_envelope(output)
        checks = envelope["data"]["checks"]
        check_names = {c["name"] for c in checks}

        # Sandbox checks
        assert "sandbox_data" in check_names
        assert "sandbox_cache" in check_names
        assert "sandbox_locks" in check_names
        assert "sandbox_crash_dumps" in check_names
        assert "sandbox_logs" in check_names

        # Config check
        assert "config_file" in check_names

        # User health check
        assert "database" in check_names

    def test_doctor_user_health_check_results(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Doctor includes user-registered health check results."""
        app, cli = full_app
        _, output = _run_agent_command(app, cli, ["agent", "doctor"])

        envelope = _first_envelope(output)
        checks = envelope["data"]["checks"]
        db_check = next(c for c in checks if c["name"] == "database")
        assert db_check["status"] == "pass"
        assert db_check["message"] == "connected"

    def test_doctor_fails_with_missing_sandbox_dirs(
        self, app: App, tmp_sandbox: Path
    ) -> None:
        """Doctor reports fail when sandbox dirs don't exist."""
        # Do NOT create sandbox dirs
        cli = _build_cli(app)
        _, output = _run_agent_command(app, cli, ["agent", "doctor"])

        envelope = _first_envelope(output)
        data = envelope["data"]
        assert data["status"] == "fail"
        sandbox_checks = [
            c for c in data["checks"] if c["name"].startswith("sandbox_")
        ]
        assert all(c["status"] == "fail" for c in sandbox_checks)


# ===========================================================================
# Test: Debug last-crash command
# ===========================================================================


class TestDebugLastCrashIntegration:
    """End-to-end tests for `agent debug last-crash` via App.run()."""

    def test_debug_last_crash_emits_result_with_dump(
        self, full_app: tuple[App, typer.Typer], tmp_sandbox: Path
    ) -> None:
        """Debug last-crash returns crash dump metadata when dumps exist."""
        app, cli = full_app

        # Write a crash dump
        crash_dir = tmp_sandbox / "crash_dumps"
        dump_data = {
            "timestamp": "2024-01-15T10:30:00Z",
            "app_name": "test-tool",
            "app_version": "1.0.0",
            "crash_type": "panic",
            "panic_value": "division by zero",
            "stack_trace": "File 'main.py', line 42\n  x / 0\nZeroDivisionError",
            "flight_context": {"last_command": "deploy"},
        }
        (crash_dir / "crash-20240115-103000.json").write_text(json.dumps(dump_data))

        code, output = _run_agent_command(app, cli, ["agent", "debug-last-crash"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        validate_envelope(envelope)

        data = envelope["data"]
        assert data["file"] == "crash-20240115-103000.json"
        assert data["crash"]["crash_type"] == "panic"
        assert data["crash"]["panic_value"] == "division by zero"
        assert "stack_trace" in data["crash"]
        assert data["crash"]["flight_context"]["last_command"] == "deploy"

    def test_debug_last_crash_newest_first(
        self, full_app: tuple[App, typer.Typer], tmp_sandbox: Path
    ) -> None:
        """Debug last-crash returns the most recent dump (by mtime)."""
        app, cli = full_app

        crash_dir = tmp_sandbox / "crash_dumps"

        old_dump = {
            "timestamp": "old",
            "crash_type": "panic",
            "stack_trace": "old trace",
            "flight_context": {},
        }
        new_dump = {
            "timestamp": "new",
            "crash_type": "signal",
            "stack_trace": "new trace",
            "flight_context": {},
        }

        old_file = crash_dir / "crash-old.json"
        new_file = crash_dir / "crash-new.json"
        old_file.write_text(json.dumps(old_dump))
        # Ensure different mtime
        time.sleep(0.05)
        new_file.write_text(json.dumps(new_dump))

        _, output = _run_agent_command(app, cli, ["agent", "debug-last-crash"])

        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        # Should return the newest file
        assert envelope["data"]["file"] == "crash-new.json"
        assert envelope["data"]["crash"]["crash_type"] == "signal"

    def test_debug_last_crash_no_dumps(
        self, full_app: tuple[App, typer.Typer], tmp_sandbox: Path
    ) -> None:
        """Debug last-crash emits NOT_FOUND error when no dumps exist."""
        app, cli = full_app

        # crash_dumps dir exists but is empty (created by _make_sandbox_dirs)
        code, output = _run_agent_command(app, cli, ["agent", "debug-last-crash"])

        assert code != EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"
        validate_envelope(envelope)

    def test_debug_last_crash_no_dir(
        self, app: App, tmp_sandbox: Path
    ) -> None:
        """Debug last-crash emits NOT_FOUND when crash_dumps dir doesn't exist."""
        # Don't create any sandbox dirs
        cli = _build_cli(app)
        code, output = _run_agent_command(app, cli, ["agent", "debug-last-crash"])

        assert code != EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "error"
        assert envelope["error_code"] == "NOT_FOUND"


# ===========================================================================
# Test: Cache clean command
# ===========================================================================


class TestCacheCleanIntegration:
    """End-to-end tests for `agent cache clean` via App.run()."""

    def test_cache_clean_removes_files(
        self, full_app: tuple[App, typer.Typer], tmp_sandbox: Path
    ) -> None:
        """Cache clean removes all files from cache dir and reports count."""
        app, cli = full_app

        cache_dir = tmp_sandbox / "cache"
        (cache_dir / "file1.txt").write_text("data1")
        (cache_dir / "file2.bin").write_text("data2")
        (cache_dir / "file3.json").write_text("{}")

        code, output = _run_agent_command(app, cli, ["agent", "cache-clean"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        assert envelope["data"]["cleaned"] == 3
        validate_envelope(envelope)

        # Verify directory is now empty
        assert not list(cache_dir.iterdir())

    def test_cache_clean_empty_directory(
        self, full_app: tuple[App, typer.Typer], tmp_sandbox: Path
    ) -> None:
        """Cache clean returns 0 for empty directory."""
        app, cli = full_app

        # cache dir exists but is empty (created by _make_sandbox_dirs)
        code, output = _run_agent_command(app, cli, ["agent", "cache-clean"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["data"]["cleaned"] == 0

    def test_cache_clean_nonexistent_directory(
        self, app: App, tmp_sandbox: Path
    ) -> None:
        """Cache clean returns 0 for nonexistent directory."""
        # Don't create sandbox dirs
        cli = _build_cli(app)
        code, output = _run_agent_command(app, cli, ["agent", "cache-clean"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["data"]["cleaned"] == 0


# ===========================================================================
# Cross-cutting: stdout hijacking, envelope validity, tool name
# ===========================================================================


class TestCrossCutting:
    """Cross-cutting integration tests covering multiple command properties."""

    def test_all_successful_commands_valid_envelopes(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Every successful command produces envelopes that pass validate_envelope()."""
        app, cli = full_app

        # Commands expected to succeed
        commands = [
            ["agent", "schema"],
            ["agent", "errors"],
            ["agent", "config", "list"],
            ["agent", "doctor"],
            ["agent", "cache-clean"],
        ]

        for cmd_args in commands:
            # Rebuild app/cli to get fresh state per command
            app_fresh = App("test-tool", "1.0.0")
            sandbox_base = Path(app._sandbox._base_dir)
            app_fresh._sandbox._base_dir = str(sandbox_base)

            cfg_path = sandbox_base / "config.json"
            _write_config(
                cfg_path,
                {"name": "test", "port": 9090, "api_key": "s", "debug": True},
            )
            mgr = ConfigManager(IntegrationConfig, str(cfg_path))
            app_fresh.register_config("main", mgr)

            def check_db() -> HealthCheckResult:
                return HealthCheckResult(name="database", status="pass", message="ok")

            app_fresh.register_health_check("database", check_db)
            app_fresh.register_command_meta(
                "test-cli deploy", CommandMeta(description="Deploy", is_idempotent=False)
            )

            cli_fresh = _build_cli(app_fresh)
            code, output = _run_agent_command(app_fresh, cli_fresh, cmd_args)

            assert code == EXIT_SUCCESS, f"{' '.join(cmd_args)} returned {code}"

            for line in output.strip().split("\n"):
                if line.strip():
                    envelope = json.loads(line)
                    assert envelope["tool"] == "test-tool", (
                        f"{' '.join(cmd_args)}: wrong tool name: {envelope.get('tool')}"
                    )
                    validate_envelope(envelope)

    def test_all_output_is_jsonl_no_leaks(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """Every line of output from each command is valid JSON — no stdout leaks."""
        app, cli = full_app

        commands = [
            ["agent", "schema"],
            ["agent", "errors"],
            ["agent", "config", "list"],
            ["agent", "doctor"],
            ["agent", "cache-clean"],
        ]

        for cmd_args in commands:
            app_fresh = App("test-tool", "1.0.0")
            sandbox_base = Path(app._sandbox._base_dir)
            app_fresh._sandbox._base_dir = str(sandbox_base)

            cfg_path = sandbox_base / "config.json"
            _write_config(
                cfg_path,
                {"name": "test", "port": 9090, "api_key": "s", "debug": True},
            )
            mgr = ConfigManager(IntegrationConfig, str(cfg_path))
            app_fresh.register_config("main", mgr)

            def check_db() -> HealthCheckResult:
                return HealthCheckResult(name="database", status="pass", message="ok")

            app_fresh.register_health_check("database", check_db)

            cli_fresh = _build_cli(app_fresh)
            _, output = _run_agent_command(app_fresh, cli_fresh, cmd_args)

            lines = output.strip().split("\n")
            for i, line in enumerate(lines):
                if line.strip():
                    # Must parse as JSON
                    try:
                        json.loads(line)
                    except json.JSONDecodeError as e:
                        pytest.fail(
                            f"{' '.join(cmd_args)}: line {i} is not valid JSON: "
                            f"{line!r} — {e}"
                        )

    def test_user_command_works_alongside_agent_commands(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """A user command (e.g. 'status') works through App.run() too."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["status"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["type"] == "result"
        assert envelope["data"]["status"] == "running"
        validate_envelope(envelope)

    def test_deploy_command_with_option(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """A user command with options works through App.run()."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["deploy", "--env", "production"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["data"]["deployed_to"] == "production"
        validate_envelope(envelope)

    def test_db_migrate_subgroup(
        self, full_app: tuple[App, typer.Typer]
    ) -> None:
        """A nested user command (db migrate) works through App.run()."""
        app, cli = full_app
        code, output = _run_agent_command(app, cli, ["db", "migrate", "--revision", "abc123"])

        assert code == EXIT_SUCCESS
        envelope = _first_envelope(output)
        assert envelope["data"]["migration"] == "abc123"
        validate_envelope(envelope)
