"""Tests for App — stdout hijacking, exception recovery, and register methods."""

from __future__ import annotations

import io
import json
import os
import sys
import tempfile
from pathlib import Path
from typing import Any
from unittest.mock import patch

import pytest
import typer

from agentsdk.app import App, _FakeStream
from agentsdk.exitcode import (
    EXIT_FATAL_ERROR,
    EXIT_SUCCESS,
    ExitError,
)
from agentsdk.flightcontext import FlightContext
from agentsdk.sandbox import Sandbox
from agentsdk.writer import Writer


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_typer_hello() -> typer.Typer:
    """Minimal Typer app with a single ``hello`` command."""
    cli = typer.Typer()

    @cli.command()
    def hello(name: str = "World"):
        print(f"Hello {name}!")

    return cli


def _run_capture(
    app: App, cli: typer.Typer, args: list[str] | None = None
) -> tuple[int, str, str]:
    """Run *app.run(cli)* and return (exit_code, jsonl_output, captured_output).

    Sets the app's ``_real_stdout`` to a StringIO so JSONL output and
    captured output can be inspected independently.
    """
    jsonl_buf = io.StringIO()
    app._real_stdout = jsonl_buf
    code = app.run(cli, args=(args if args is not None else []))
    return code, jsonl_buf.getvalue(), app.captured_output


# ========================================================================
# _FakeStream
# ========================================================================


class TestFakeStream:
    def test_write_appends(self):
        f = _FakeStream()
        f.write("hello ")
        f.write("world")
        assert f.getvalue() == "hello world"

    def test_flush_is_noop(self):
        f = _FakeStream()
        f.flush()  # should not raise

    def test_isatty_returns_false(self):
        f = _FakeStream()
        assert f.isatty() is False

    def test_getvalue_empty(self):
        f = _FakeStream()
        assert f.getvalue() == ""

    def test_composition_not_inheritance(self):
        """_FakeStream must NOT inherit from io.TextIOBase."""
        f = _FakeStream()
        assert not isinstance(f, io.TextIOBase)


# ========================================================================
# App.__init__ and properties
# ========================================================================


class TestAppInit:
    def test_name_and_version(self):
        app = App("my-tool", "2.5.0")
        assert app.name == "my-tool"
        assert app.version == "2.5.0"

    def test_writer_is_writer_instance(self):
        app = App("x", "1.0")
        assert isinstance(app.writer, Writer)

    def test_sandbox_is_sandbox_instance(self):
        app = App("x", "1.0")
        assert isinstance(app.sandbox, Sandbox)

    def test_flight_context_is_flight_context_instance(self):
        app = App("x", "1.0")
        assert isinstance(app.flight_context, FlightContext)

    def test_captured_output_empty_before_run(self):
        app = App("x", "1.0")
        assert app.captured_output == ""

    def test_trace_id_from_env(self):
        with patch.dict(os.environ, {"AGENT_TRACE_ID": "trace-123"}):
            app = App("x", "1.0")
            assert app._trace_id == "trace-123"

    def test_trace_id_empty_when_not_set(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            env = {"HOME": tmpdir, "USERPROFILE": tmpdir}
            os.environ.pop("AGENT_TRACE_ID", None)
            with patch.dict(os.environ, env, clear=False):
                app = App("x", "1.0")
                assert app._trace_id == ""


# ========================================================================
# set_writer
# ========================================================================


class TestSetWriter:
    def test_replaces_writer(self):
        app = App("x", "1.0")
        buf = io.StringIO()
        new_writer = Writer(buf, tool_name="test")
        app.set_writer(new_writer)
        assert app.writer is new_writer

    def test_injected_writer_emits(self):
        app = App("x", "1.0")
        buf = io.StringIO()
        new_writer = Writer(buf, tool_name="test")
        app.set_writer(new_writer)
        app.writer.success({"ok": True})
        data = json.loads(buf.getvalue().strip())
        assert data["type"] == "result"


# ========================================================================
# Register methods
# ========================================================================


class TestRegisterMethods:
    def test_register_error_code(self):
        app = App("x", "1.0")
        app.register_error_code("MY_ERR", 42, "custom error")
        assert app.error_code_to_exit_code("MY_ERR") == 42

    def test_register_error_code_builtin_raises(self):
        app = App("x", "1.0")
        with pytest.raises(ValueError, match="Cannot override built-in"):
            app.register_error_code("FATAL_CRASH", 99, "nope")

    def test_error_code_to_exit_code_unknown(self):
        app = App("x", "1.0")
        assert app.error_code_to_exit_code("NONEXISTENT") == EXIT_FATAL_ERROR

    def test_register_config(self):
        app = App("x", "1.0")
        provider = lambda: {"k": "v"}  # noqa: E731
        app.register_config("my-config", provider)
        assert app._config_providers["my-config"] is provider

    def test_register_health_check(self):
        app = App("x", "1.0")
        check = lambda: True  # noqa: E731
        app.register_health_check("db", check)
        assert app._health_checks["db"] is check

    def test_register_command_meta(self):
        app = App("x", "1.0")
        meta = {"timeout": 30}
        app.register_command_meta("deploy status", meta)
        assert app._command_meta["deploy status"] is meta


# ========================================================================
# App.run() — success path
# ========================================================================


class TestRunSuccess:
    def test_successful_command_returns_zero(self):
        app = App("test-app", "1.0.0")
        cli = _make_typer_hello()
        code, jsonl, captured = _run_capture(app, cli, ["--name", "Test"])
        assert code == EXIT_SUCCESS

    def test_stdout_hijacking_captures_print(self):
        """Non-SDK output (print) should go to _FakeStream, not the writer."""
        app = App("test-app", "1.0.0")
        cli = _make_typer_hello()
        code, jsonl, captured = _run_capture(app, cli, ["--name", "Test"])
        assert code == EXIT_SUCCESS
        # The print("Hello Test!") goes to _FakeStream
        assert "Hello Test!" in captured

    def test_no_jsonl_on_success(self):
        """A clean command produces no JSONL envelopes."""
        app = App("test-app", "1.0.0")
        cli = _make_typer_hello()
        code, jsonl, captured = _run_capture(app, cli, ["--name", "Test"])
        assert code == EXIT_SUCCESS
        assert jsonl.strip() == ""

    def test_stdout_restored_after_run(self):
        """sys.stdout must be restored to _real_stdout after run."""
        app = App("test-app", "1.0.0")
        real_out = app._real_stdout
        cli = _make_typer_hello()
        app.run(cli, args=["--name", "Test"])
        assert sys.stdout is real_out

    def test_stdout_restored_after_panic(self):
        """sys.stdout must be restored even after a panic."""
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("oops")

        app = App("test-app", "1.0.0")
        real_out = app._real_stdout
        jsonl_buf = io.StringIO()
        app._real_stdout = jsonl_buf
        app.run(cli, args=[])
        assert sys.stdout is jsonl_buf


# ========================================================================
# App.run() — ExitError
# ========================================================================


class TestRunExitError:
    def test_exit_error_returns_code(self):
        cli = typer.Typer()

        @cli.command()
        def boom():
            raise ExitError(42, "test exit", None)

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == 42

    def test_exit_error_no_crash_dump(self):
        """ExitError should NOT produce a FATAL_CRASH envelope."""
        cli = typer.Typer()

        @cli.command()
        def boom():
            raise ExitError(3, "not found", None)

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == 3
        assert "FATAL_CRASH" not in jsonl

    def test_exit_error_with_original_error(self):
        """ExitError carries its wrapped error."""
        cli = typer.Typer()

        @cli.command()
        def boom():
            raise ExitError(2, "bad input", ValueError("detail"))

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == 2


# ========================================================================
# App.run() — Panic recovery
# ========================================================================


class TestRunPanicRecovery:
    def test_runtime_error_produces_fatal_crash(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("something broke")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == EXIT_FATAL_ERROR
        assert "FATAL_CRASH" in jsonl

    def test_fatal_crash_envelope_has_traceback(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise ValueError("bad value")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        data = json.loads(jsonl.strip())
        assert data["type"] == "error"
        assert data["error_code"] == "FATAL_CRASH"
        assert "bad value" in data["message"]
        assert "ValueError" in data["message"]

    def test_fatal_crash_writes_crash_dump_file(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("boom")

        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(os.environ, {"TEST_APP_HOME": tmpdir}):
                app = App("test-app", "1.0.0")
                jsonl_buf = io.StringIO()
                app._real_stdout = jsonl_buf
                code = app.run(cli, args=[])
                assert code == EXIT_FATAL_ERROR

                # Check crash dump file exists
                crash_dir = Path(tmpdir) / "crash_dumps"
                assert crash_dir.exists()
                dumps = list(crash_dir.glob("crash-*.json"))
                assert len(dumps) == 1

                # Verify crash dump content
                dump_data = json.loads(dumps[0].read_text(encoding="utf-8"))
                assert dump_data["app_name"] == "test-app"
                assert dump_data["app_version"] == "1.0.0"
                assert dump_data["crash_type"] == "panic"
                assert "boom" in dump_data["panic_value"]
                assert "RuntimeError" in dump_data["stack_trace"]

    def test_crash_dump_includes_flight_context(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("context test")

        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(os.environ, {"TEST_APP_HOME": tmpdir}):
                app = App("test-app", "1.0.0")
                app.flight_context.set("current_step", "deploying")
                app.flight_context.set("target", "prod")

                jsonl_buf = io.StringIO()
                app._real_stdout = jsonl_buf
                app.run(cli, args=[])

                crash_dir = Path(tmpdir) / "crash_dumps"
                dump_data = json.loads(
                    list(crash_dir.glob("crash-*.json"))[0].read_text()
                )
                assert dump_data["flight_context"]["current_step"] == "deploying"
                assert dump_data["flight_context"]["target"] == "prod"

    def test_stdout_restored_after_panic_dedicated(self):
        """sys.stdout must be restored even after a panic (no _run_capture)."""
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("oops")

        app = App("test-app", "1.0.0")
        real_out = app._real_stdout
        jsonl_buf = io.StringIO()
        app._real_stdout = jsonl_buf
        app.run(cli, args=[])
        assert sys.stdout is jsonl_buf

    def test_crash_dump_includes_trace_id(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("trace test")

        with tempfile.TemporaryDirectory() as tmpdir:
            with patch.dict(
                os.environ,
                {"TEST_APP_HOME": tmpdir, "AGENT_TRACE_ID": "trace-abc-123"},
            ):
                app = App("test-app", "1.0.0")
                jsonl_buf = io.StringIO()
                app._real_stdout = jsonl_buf
                app.run(cli, args=[])

                crash_dir = Path(tmpdir) / "crash_dumps"
                dump_data = json.loads(
                    list(crash_dir.glob("crash-*.json"))[0].read_text()
                )
                assert dump_data["trace_id"] == "trace-abc-123"

    def test_jsonl_envelope_has_trace_id(self):
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("envelope trace")

        with patch.dict(os.environ, {"AGENT_TRACE_ID": "t-999"}):
            app = App("test-app", "1.0.0")
            jsonl_buf = io.StringIO()
            app._real_stdout = jsonl_buf
            app.run(cli, args=[])
            data = json.loads(jsonl_buf.getvalue().strip())
            assert data["trace_id"] == "t-999"


# ========================================================================
# App.run() — --quiet flag
# ========================================================================


class TestRunQuietFlag:
    def test_quiet_flag_available(self):
        """The --quiet flag should be added to the CLI without error."""
        app = App("test-app", "1.0.0")
        cli = _make_typer_hello()
        jsonl_buf = io.StringIO()
        app._real_stdout = jsonl_buf
        # Run with --help — should not raise
        code = app.run(cli, args=["--help"])
        assert code == EXIT_SUCCESS

    def test_quiet_flag_sets_writer_quiet(self):
        """Passing --quiet should call writer.set_quiet(True)."""
        cli = typer.Typer()

        @cli.command()
        def check():
            pass

        app = App("test-app", "1.0.0")
        jsonl_buf = io.StringIO()
        app._real_stdout = jsonl_buf

        # Use a dict to capture the writer's quiet state at command time.
        quiet_at_run: dict[str, bool] = {}

        _original_set_quiet = Writer.set_quiet

        def spy_set_quiet(self_writer: Writer, quiet: bool) -> None:
            quiet_at_run["value"] = quiet
            return _original_set_quiet(self_writer, quiet)

        with patch.object(Writer, "set_quiet", spy_set_quiet):
            app.run(cli, args=["--quiet"])

        assert quiet_at_run.get("value") is True

    def test_no_quiet_flag_leaves_writer_noisy(self):
        """Without --quiet, writer should be set to quiet=False."""
        cli = typer.Typer()

        @cli.command()
        def check():
            pass

        app = App("test-app", "1.0.0")
        jsonl_buf = io.StringIO()
        app._real_stdout = jsonl_buf

        quiet_at_run: dict[str, bool] = {}

        _original_set_quiet = Writer.set_quiet

        def spy_set_quiet(self_writer: Writer, quiet: bool) -> None:
            quiet_at_run["value"] = quiet
            return _original_set_quiet(self_writer, quiet)

        with patch.object(Writer, "set_quiet", spy_set_quiet):
            app.run(cli, args=[])

        assert quiet_at_run.get("value") is False


# ========================================================================
# App.run() — signal handler lifecycle
# ========================================================================


class TestSignalHandlerLifecycle:
    def test_signal_handler_started_and_stopped(self):
        """run() should start and stop the signal handler."""
        cli = _make_typer_hello()
        app = App("test-app", "1.0.0")

        lifecycle: list[str] = []

        def mock_setup(*a, **kw):
            lifecycle.append("setup")
            return lambda: lifecycle.append("stop")

        with patch("agentsdk.app.setup_signal_handler", mock_setup):
            _run_capture(app, cli)

        assert lifecycle == ["setup", "stop"]

    def test_signal_handler_stopped_on_panic(self):
        """Signal handler cleanup must run even on panic."""
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("boom")

        app = App("test-app", "1.0.0")
        lifecycle: list[str] = []

        def mock_setup(*a, **kw):
            lifecycle.append("setup")
            return lambda: lifecycle.append("stop")

        with patch("agentsdk.app.setup_signal_handler", mock_setup):
            _run_capture(app, cli)

        assert lifecycle == ["setup", "stop"]


# ========================================================================
# App.run() — stdout hijacking edge cases
# ========================================================================


class TestStdoutHijacking:
    def test_multiple_prints_captured(self):
        """All print output during run() is captured."""
        cli = typer.Typer()

        @cli.command()
        def multi():
            print("line 1")
            print("line 2")
            print("line 3")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert "line 1" in captured
        assert "line 2" in captured
        assert "line 3" in captured

    def test_jsonl_goes_to_real_stdout_not_fake_stream(self):
        """JSONL from the writer should go to _real_stdout, not _FakeStream."""
        cli = typer.Typer()

        @cli.command()
        def emit():
            print("user output")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        # "user output" is captured by the fake stream
        assert "user output" in captured
        # JSONL output should be clean (no user output mixed in)
        # Since the command just prints, no JSONL envelopes are emitted
        assert jsonl.strip() == ""

    def test_panic_jsonl_goes_to_real_stdout(self):
        """FATAL_CRASH JSONL should go to _real_stdout, not _FakeStream."""
        cli = typer.Typer()

        @cli.command()
        def crash():
            print("before crash")
            raise RuntimeError("boom")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        # Print output captured by fake stream
        assert "before crash" in captured
        # FATAL_CRASH in JSONL (real stdout)
        assert "FATAL_CRASH" in jsonl
        # The JSONL output should NOT contain the print output
        for line in jsonl.strip().splitlines():
            if line:
                data = json.loads(line)
                assert "before crash" not in str(data)


# ========================================================================
# App.run() — crash dump write failure
# ========================================================================


class TestCrashDumpWriteFailure:
    def test_fatal_crash_emitted_even_if_crash_dump_fails(self):
        """If crash dump write fails, FATAL_CRASH JSONL must still be emitted."""
        cli = typer.Typer()

        @cli.command()
        def crash():
            raise RuntimeError("test")

        app = App("test-app", "1.0.0")

        with patch("agentsdk.app.write_crash_dump", side_effect=OSError("disk full")):
            code, jsonl, captured = _run_capture(app, cli)

        assert code == EXIT_FATAL_ERROR
        assert "FATAL_CRASH" in jsonl


# ========================================================================
# App.run() — SystemExit handling
# ========================================================================


class TestSystemExit:
    def test_system_exit_zero(self):
        """SystemExit(0) should return 0."""
        cli = typer.Typer()

        @cli.command()
        def noop():
            sys.exit(0)

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == 0

    def test_system_exit_nonzero(self):
        """SystemExit(N) should return N."""
        cli = typer.Typer()

        @cli.command()
        def exit3():
            sys.exit(3)

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == 3

    def test_system_exit_no_crash_dump(self):
        """SystemExit should NOT produce a crash dump."""
        cli = typer.Typer()

        @cli.command()
        def exit0():
            sys.exit(0)

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert "FATAL_CRASH" not in jsonl

    def test_system_exit_string_code(self):
        """SystemExit with non-int code should return EXIT_SUCCESS."""
        cli = typer.Typer()

        @cli.command()
        def exit_str():
            sys.exit("error message")

        app = App("test-app", "1.0.0")
        code, jsonl, captured = _run_capture(app, cli)
        assert code == EXIT_SUCCESS
