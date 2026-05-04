"""Agent meta-commands — Typer subcommands for agent schema, errors, config, doctor, debug, and cache.

Creates a Typer app with ``name="agent"`` containing six subcommands that
produce structured JSONL output.  Each command uses the App's Writer for
emission (never direct print).

Commands:
    agent schema        — walk the Click command tree and emit schema JSON
    agent errors        — list all registered error codes
    agent config list   — show redacted configuration
    agent config set    — set a configuration value
    agent doctor        — run health checks
    agent debug last-crash — show the most recent crash dump
    agent cache clean   — clear the cache directory
"""

from __future__ import annotations

import json
import os
from pathlib import Path
from typing import Any, Dict, List, Optional

import click
import typer

from agentsdk.command_meta import CommandMeta
from agentsdk.config import ConfigProvider
from agentsdk.exitcode import EXIT_FATAL_ERROR, EXIT_NOT_FOUND, EXIT_SUCCESS, ExitError
from agentsdk.health import HealthCheckFunc, HealthCheckResult


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _first_config_provider(app: Any) -> Optional[ConfigProvider]:
    """Return the first registered ConfigProvider, or None."""
    providers = getattr(app, "_config_providers", {})
    if not providers:
        return None
    # Return the first value (insertion-order since Python 3.7).
    return next(iter(providers.values()))


def _walk_commands(
    click_cmd: click.BaseCommand,
    parent_path: str = "",
) -> List[Dict[str, Any]]:
    """Recursively flatten a Click command tree into schema entries.

    Each entry contains ``name``, ``path``, and any discovered flags/options.
    Groups (``click.MultiCommand``) are traversed; leaf commands produce
    entries.
    """
    entries: List[Dict[str, Any]] = []

    name = getattr(click_cmd, "name", None) or ""
    full_path = f"{parent_path} {name}".strip() if parent_path else name

    if isinstance(click_cmd, click.Group):
        # The group itself is an entry if it has a name.
        if name:
            entry: Dict[str, Any] = {"name": name, "path": full_path}
            flags = _extract_flags(click_cmd)
            if flags:
                entry["flags"] = flags
            entries.append(entry)

        # Recurse into children.
        ctx_obj = click.Context(click_cmd, info_name=name or None)
        for child_name in click_cmd.list_commands(ctx_obj):
            child_cmd = click_cmd.get_command(ctx_obj, child_name)
            if child_cmd is not None:
                child_path = full_path if full_path else ""
                entries.extend(_walk_commands(child_cmd, child_path))
    else:
        # Leaf command.
        if name:
            entry = {"name": name, "path": full_path}
            flags = _extract_flags(click_cmd)
            if flags:
                entry["flags"] = flags
            entries.append(entry)

    return entries


def _extract_flags(cmd: click.BaseCommand) -> List[Dict[str, Any]]:
    """Extract flag/option metadata from a Click command."""
    flags: List[Dict[str, Any]] = []
    params = getattr(cmd, "params", None) or []
    for param in params:
        if isinstance(param, click.Option):
            flag_info: Dict[str, Any] = {
                "name": param.name or "",
                "opts": list(param.opts),
            }
            if param.is_flag:
                flag_info["is_flag"] = True
            if param.default is not None:
                flag_info["default"] = param.default
            if param.help:
                flag_info["help"] = param.help
            flags.append(flag_info)
    return flags


# ---------------------------------------------------------------------------
# Command implementations
# ---------------------------------------------------------------------------


def _schema_command(app: Any, ctx: typer.Context) -> None:
    """Walk the Click command tree and emit schema JSONL result."""
    import typer as _typer

    # Get the root Click command from the parent Typer app.
    # We need to walk the original CLI's command tree, not the agent commands themselves.
    # The root is found by walking up to the root of the Click context chain.
    click_ctx = ctx.parent
    while click_ctx and click_ctx.parent is not None:
        click_ctx = click_ctx.parent

    root_cmd = click_ctx.command if click_ctx else None

    entries = []
    if root_cmd is not None:
        entries = _walk_commands(root_cmd)

    # Merge CommandMeta registrations.
    command_meta: Dict[str, CommandMeta] = getattr(app, "_command_meta", {})
    for entry in entries:
        path = entry.get("path", "")
        if path in command_meta:
            meta = command_meta[path]
            entry["description"] = meta.description
            entry["is_idempotent"] = meta.is_idempotent

    result = {
        "tool": app.name,
        "version": app.version,
        "commands": entries,
    }
    app.writer.success(result)


def _errors_command(app: Any) -> None:
    """List all registered error codes, sorted alphabetically."""
    registry = getattr(app, "_registry", None)
    if registry is None:
        app.writer.success({"codes": [], "count": 0})
        return

    all_codes = registry.all_codes()
    codes_list = []
    for code in sorted(all_codes.keys()):
        exit_code, description = all_codes[code]
        codes_list.append({
            "code": code,
            "exit_code": exit_code,
            "description": description,
        })

    app.writer.success({"codes": codes_list, "count": len(codes_list)})


def _config_list_command(app: Any) -> None:
    """Emit redacted configuration for the first registered ConfigProvider."""
    provider = _first_config_provider(app)
    if provider is None:
        app.writer.error_with_code(
            "NOT_FOUND",
            "no config provider registered",
        )
        raise ExitError(EXIT_NOT_FOUND, "no config provider registered")

    try:
        data = provider.list_redacted()
        app.writer.success({"config": data})
    except Exception as exc:
        app.writer.error_with_code(
            "INTERNAL_ERROR",
            f"failed to read config: {exc}",
        )
        raise ExitError(EXIT_FATAL_ERROR, f"failed to read config: {exc}") from exc


def _config_set_command(app: Any, json_path: str, value: str) -> None:
    """Set a configuration value via the first registered ConfigProvider."""
    provider = _first_config_provider(app)
    if provider is None:
        app.writer.error_with_code(
            "NOT_FOUND",
            "no config provider registered",
        )
        raise ExitError(EXIT_NOT_FOUND, "no config provider registered")

    # Validate json_path against whitelist.
    wl = provider.whitelist()
    if json_path not in wl:
        app.writer.error_with_code(
            "INPUT_INVALID",
            f"field {json_path!r} is not user-configurable",
        )
        raise ExitError(
            EXIT_FATAL_ERROR,
            f"field {json_path!r} is not user-configurable",
        )

    try:
        provider.set(json_path, value)
        app.writer.success({"set": {json_path: value}})
    except ValueError as exc:
        app.writer.error_with_code("INPUT_INVALID", str(exc))
        raise ExitError(EXIT_FATAL_ERROR, str(exc)) from exc
    except Exception as exc:
        app.writer.error_with_code(
            "INTERNAL_ERROR",
            f"failed to set config: {exc}",
        )
        raise ExitError(EXIT_FATAL_ERROR, f"failed to set config: {exc}") from exc


def _doctor_command(app: Any) -> None:
    """Run built-in and user-registered health checks, emit results."""
    checks: List[Dict[str, Any]] = []

    # Built-in: sandbox directory checks.
    sandbox = app.sandbox
    for name, dir_path in sandbox.dirs().items():
        exists = Path(dir_path).is_dir()
        checks.append({
            "name": f"sandbox_{name}",
            "status": "pass" if exists else "fail",
            "message": "" if exists else f"{dir_path} does not exist",
        })

    # Built-in: config file check (if a provider is registered).
    provider = _first_config_provider(app)
    if provider is not None:
        # Try to load config to verify it exists and parses.
        try:
            provider.list_redacted()
            checks.append({
                "name": "config_file",
                "status": "pass",
                "message": "config file is valid",
            })
        except FileNotFoundError:
            checks.append({
                "name": "config_file",
                "status": "fail",
                "message": "config file not found",
            })
        except Exception as exc:
            checks.append({
                "name": "config_file",
                "status": "fail",
                "message": f"config file error: {exc}",
            })

    # User-registered health checks.
    health_checks: Dict[str, HealthCheckFunc] = getattr(app, "_health_checks", {})
    for check_name, fn in health_checks.items():
        try:
            result: HealthCheckResult = fn()
            checks.append(result.to_dict())
        except Exception as exc:
            checks.append({
                "name": check_name,
                "status": "fail",
                "message": f"health check raised: {exc}",
            })

    # Determine overall status.
    statuses = [c["status"] for c in checks]
    if "fail" in statuses:
        overall = "fail"
    elif "warning" in statuses:
        overall = "warning"
    else:
        overall = "pass"

    app.writer.success({
        "status": overall,
        "checks": checks,
    })


def _debug_last_crash_command(app: Any) -> None:
    """Read the newest crash dump from crash_dumps/ and emit it."""
    crash_dir = Path(app.sandbox.crash_dumps_dir)

    if not crash_dir.is_dir():
        app.writer.error_with_code("NOT_FOUND", "no crash dumps directory")
        raise ExitError(EXIT_NOT_FOUND, "no crash dumps directory")

    # Find newest .json file (ignore .tmp).
    json_files = sorted(
        (f for f in crash_dir.iterdir() if f.suffix == ".json" and f.name.endswith(".json")),
        key=lambda f: f.stat().st_mtime,
        reverse=True,
    )

    if not json_files:
        app.writer.error_with_code("NOT_FOUND", "no crash dumps found")
        raise ExitError(EXIT_NOT_FOUND, "no crash dumps found")

    newest = json_files[0]
    try:
        raw = newest.read_text(encoding="utf-8")
        data = json.loads(raw)
        app.writer.success({
            "file": newest.name,
            "crash": data,
        })
    except Exception as exc:
        app.writer.error_with_code(
            "INTERNAL_ERROR",
            f"failed to read crash dump: {exc}",
        )
        raise ExitError(
            EXIT_FATAL_ERROR,
            f"failed to read crash dump: {exc}",
        ) from exc


def _cache_clean_command(app: Any) -> None:
    """Remove all files from the cache directory."""
    cache_dir = Path(app.sandbox.cache_dir)

    if not cache_dir.is_dir():
        # Nonexistent dir → nothing to clean.
        app.writer.success({"cleaned": 0})
        return

    count = 0
    for item in cache_dir.iterdir():
        try:
            if item.is_file() or item.is_symlink():
                item.unlink()
                count += 1
        except OSError:
            pass  # Best-effort cleanup.

    app.writer.success({"cleaned": count})


# ---------------------------------------------------------------------------
# Public API
# ---------------------------------------------------------------------------


def create_agent_app(app: Any) -> typer.Typer:
    """Create a Typer app with all 6 agent meta-commands.

    Parameters
    ----------
    app:
        The SDK :class:`App` instance.  Stored as closure state so each
        command callback can access it without a Typer context.

    Returns
    -------
    typer.Typer
        A Typer app with ``name="agent"`` containing six subcommands.
    """
    agent = typer.Typer(name="agent", help="Agent meta-commands")

    @agent.command("schema")
    def schema(ctx: typer.Context) -> None:
        """Walk the command tree and emit schema JSON."""
        _schema_command(app, ctx)

    @agent.command("errors")
    def errors() -> None:
        """List all registered error codes."""
        _errors_command(app)

    # Config group: `agent config list` / `agent config set`
    config_app = typer.Typer(name="config", help="Configuration management")

    @config_app.command("list")
    def config_list() -> None:
        """Show redacted configuration."""
        _config_list_command(app)

    @config_app.command("set")
    def config_set(
        json_path: str = typer.Argument(..., help="JSON path to set"),
        value: str = typer.Argument(..., help="Value to set"),
    ) -> None:
        """Set a configuration value."""
        _config_set_command(app, json_path, value)

    agent.add_typer(config_app, name="config")

    @agent.command("doctor")
    def doctor() -> None:
        """Run health checks."""
        _doctor_command(app)

    @agent.command("debug-last-crash")
    def debug_last_crash() -> None:
        """Show the most recent crash dump."""
        _debug_last_crash_command(app)

    @agent.command("cache-clean")
    def cache_clean() -> None:
        """Clear the cache directory."""
        _cache_clean_command(app)

    return agent
