"""End-to-end tests for the hello-world example CLI.

Mirrors the Go SDK's examples/helloworld/main_test.go.  Each test
builds an isolated App + CLI, invokes a command through ``app.run()``,
and asserts on the JSONL envelopes written to the captured output buffer.
"""

from __future__ import annotations

import io
import json
import os
from typing import List

import pytest

from agentsdk import App, EXIT_FATAL_ERROR, EXIT_INVALID_PARAMS, EXIT_SUCCESS, validate_envelope
from agentsdk.testing import must_parse_envelope, must_parse_envelopes

# Import the example's build_app and HelloConfig.
from examples.python.helloworld.main import HelloConfig, build_app


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _run_cmd(
    app: App,
    cli,
    args: List[str],
    tmp_path,
    config_json: str | None = None,
) -> tuple[int, str]:
    """Run a command through ``app.run()``, capturing all JSONL output.

    Sets ``app._real_stdout`` to a :class:`io.StringIO` so envelopes
    emitted during ``run()`` are captured instead of going to the real
    stdout.  Also wires the sandbox to *tmp_path* for filesystem
    isolation.

    Returns ``(exit_code, captured_output)``.
    """
    buf = io.StringIO()
    app._real_stdout = buf

    # Override sandbox base dir to use the temp directory.
    os.environ["HELLO_AGENT_HOME"] = str(tmp_path)

    # Optionally create a config file in the temp dir.
    if config_json is not None:
        data_dir = tmp_path / "data"
        data_dir.mkdir(parents=True, exist_ok=True)
        (data_dir / "config.json").write_text(config_json, encoding="utf-8")

    code = app.run(cli, args=args)
    output = buf.getvalue()

    # Clean up env var.
    os.environ.pop("HELLO_AGENT_HOME", None)

    return code, output


# ---------------------------------------------------------------------------
# Tests — basic commands
# ---------------------------------------------------------------------------


class TestGreet:
    """Tests for the ``greet`` command."""

    def test_greet_with_name(self, tmp_path):
        """greet Alice → result envelope with 'Hello, Alice!'."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["greet", "Alice"], tmp_path)

        assert code == EXIT_SUCCESS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "result"
        assert env["data"]["greeting"] == "Hello, Alice!"

    def test_greet_default(self, tmp_path):
        """greet (no args) → result envelope with 'Hello, world!'."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["greet"], tmp_path)

        assert code == EXIT_SUCCESS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["data"]["greeting"] == "Hello, world!"


class TestFail:
    """Tests for the ``fail`` command."""

    def test_fail(self, tmp_path):
        """fail → error envelope with INPUT_INVALID code."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["fail"], tmp_path)

        assert code == EXIT_INVALID_PARAMS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "error"
        assert env["error_code"] == "INPUT_INVALID"
        assert "this command always fails" in env["message"]


class TestProgress:
    """Tests for the ``progress`` command."""

    def test_progress(self, tmp_path):
        """progress → 2 progress envelopes + 1 result envelope."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["progress"], tmp_path)

        assert code == EXIT_SUCCESS
        envs = must_parse_envelopes(output)
        assert len(envs) == 3

        # First two are progress.
        for i, env in enumerate(envs[:2]):
            validate_envelope(env)
            assert env["type"] == "progress"

        # Last is result.
        validate_envelope(envs[2])
        assert envs[2]["type"] == "result"
        assert envs[2]["data"]["status"] == "complete"


class TestWarn:
    """Tests for the ``warn`` command."""

    def test_warn(self, tmp_path):
        """warn → 1 warning envelope + 1 result envelope."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["warn"], tmp_path)

        assert code == EXIT_SUCCESS
        envs = must_parse_envelopes(output)
        assert len(envs) == 2

        validate_envelope(envs[0])
        assert envs[0]["type"] == "warning"
        assert envs[0]["message"] == "something seems off"

        validate_envelope(envs[1])
        assert envs[1]["type"] == "result"
        assert envs[1]["data"]["status"] == "warned"


class TestPanic:
    """Tests for the ``panic`` command."""

    def test_panic_recovery(self, tmp_path):
        """panic → FATAL_CRASH error envelope."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["panic"], tmp_path)

        assert code == EXIT_FATAL_ERROR
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "error"
        assert env["error_code"] == "FATAL_CRASH"
        assert "intentional panic for demonstration" in env["message"]


class TestQuietMode:
    """Tests for --quiet flag suppressing progress/warning envelopes."""

    def test_quiet_with_progress(self, tmp_path):
        """--quiet progress → only result envelope survives."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["--quiet", "progress"], tmp_path)

        assert code == EXIT_SUCCESS
        envs = must_parse_envelopes(output)
        assert len(envs) == 1
        assert envs[0]["type"] == "result"

    def test_quiet_with_warning(self, tmp_path):
        """--quiet warn → only result envelope survives."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["--quiet", "warn"], tmp_path)

        assert code == EXIT_SUCCESS
        envs = must_parse_envelopes(output)
        assert len(envs) == 1
        assert envs[0]["type"] == "result"


# ---------------------------------------------------------------------------
# Tests — agent meta-commands
# ---------------------------------------------------------------------------


class TestAgentCommands:
    """Tests for agent schema, errors, and config commands."""

    def test_agent_schema(self, tmp_path):
        """agent schema → valid schema output with tool name and commands."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["agent", "schema"], tmp_path)

        assert code == EXIT_SUCCESS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "result"
        assert env["data"]["tool"] == "hello-agent"
        assert isinstance(env["data"]["commands"], list)
        assert len(env["data"]["commands"]) > 0

    def test_agent_errors(self, tmp_path):
        """agent errors → valid error codes output."""
        app, cli = build_app(config_path=str(tmp_path / "cfg.json"))
        code, output = _run_cmd(app, cli, ["agent", "errors"], tmp_path)

        assert code == EXIT_SUCCESS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "result"
        assert isinstance(env["data"]["codes"], list)
        assert len(env["data"]["codes"]) > 0
        assert env["data"]["count"] > 0

    def test_agent_config_list_redacted(self, tmp_path):
        """agent config list → redacted config (api_key hidden)."""
        config_json = json.dumps(
            {"name": "test-agent", "language": "en", "api_key": "super-secret-key"}
        )

        app, cli = build_app(config_path=str(tmp_path / "data" / "config.json"))
        code, output = _run_cmd(
            app, cli, ["agent", "config", "list"], tmp_path, config_json=config_json
        )

        assert code == EXIT_SUCCESS
        env = must_parse_envelope(output.strip())
        validate_envelope(env)
        assert env["type"] == "result"

        config = env["data"]["config"]
        assert config["name"] == "test-agent"
        assert config["language"] == "en"
        # Sensitive field must be redacted.
        assert config["api_key"] == "***"
