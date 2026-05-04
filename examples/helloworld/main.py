"""Hello-world example CLI demonstrating the full agentsdk lifecycle.

Creates a Typer-based CLI with greet, fail, progress, warn, and panic
commands, plus all agent meta-commands (schema, errors, config, etc.).
Each command produces structured JSONL output via the SDK Writer.

Run with::

    python -m examples.helloworld.main greet Alice
    python -m examples.helloworld.main fail
    python -m examples.helloworld.main agent schema
"""

from __future__ import annotations

import os
from typing import Optional

import typer
from pydantic import BaseModel, Field

from agentsdk import App, ConfigManager, ExitError, EXIT_INVALID_PARAMS


# ---------------------------------------------------------------------------
# Config model
# ---------------------------------------------------------------------------


class HelloConfig(BaseModel):
    """Sample configuration with sensitive and configurable fields."""

    name: str = Field(default="world", json_schema_extra={"config": True})
    language: str = Field(default="en", json_schema_extra={"config": True})
    api_key: str = Field(
        default="",
        alias="api_key",
        json_schema_extra={"sensitive": True},
    )


# ---------------------------------------------------------------------------
# Build the CLI
# ---------------------------------------------------------------------------


def build_app(config_path: Optional[str] = None) -> tuple[App, typer.Typer]:
    """Create an App and its Typer CLI, returning both.

    Parameters
    ----------
    config_path:
        Path to the JSON config file.  When ``None``, falls back to
        ``<sandbox data dir>/config.json`` so tests can override via the
        ``HELLO_AGENT_HOME`` environment variable.
    """
    app = App("hello-agent", "1.0.0")

    # --- Config setup ---
    if config_path is None:
        config_path = os.path.join(app.sandbox.data_dir, "config.json")
    cfg_mgr = ConfigManager[HelloConfig](HelloConfig, config_path)
    app.register_config("default", cfg_mgr)

    # --- Root CLI ---
    cli = typer.Typer(
        name="hello-agent",
        help="A hello-world agent CLI",
        no_args_is_help=True,
    )

    # greet command — emits a JSONL result envelope with a greeting.
    @cli.command()
    def greet(name: str = typer.Argument(default="world", help="Name to greet")) -> None:
        """Greet someone."""
        app.writer.success({"greeting": f"Hello, {name}!"})

    # fail command — demonstrates error output.
    @cli.command()
    def fail() -> None:
        """Demonstrate error output."""
        app.writer.error_with_code("INPUT_INVALID", "this command always fails")
        raise ExitError(EXIT_INVALID_PARAMS, "this command always fails")

    # progress command — emits a progress sequence then a result.
    @cli.command()
    def progress() -> None:
        """Demonstrate progress output."""
        app.writer.progress(50, "halfway there")
        app.writer.progress(100, "done")
        app.writer.success({"status": "complete"})

    # warn command — emits a warning and a result.
    @cli.command()
    def warn() -> None:
        """Demonstrate warning output."""
        app.writer.warning("something seems off")
        app.writer.success({"status": "warned"})

    # panic command — demonstrates panic recovery / FATAL_CRASH envelope.
    @cli.command()
    def panic() -> None:
        """Demonstrate panic recovery."""
        raise RuntimeError("intentional panic for demonstration")

    # --- Agent meta-commands ---
    cli.add_typer(app.agent_commands(), name="agent")

    return app, cli


# ---------------------------------------------------------------------------
# Entry point
# ---------------------------------------------------------------------------


def main() -> None:
    """Build and run the hello-agent CLI."""
    app, cli = build_app()
    app.run(cli)


if __name__ == "__main__":
    main()
