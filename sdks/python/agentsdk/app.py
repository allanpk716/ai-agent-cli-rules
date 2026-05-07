"""App — the main SDK entry point.

Composes Writer, Sandbox, FlightContext, ErrorCodeRegistry, CrashDump,
and SignalHandler into a single orchestration layer. The ``run()`` method
provides stdout hijacking, exception recovery, and signal handling for
Typer-based CLI applications.

**Stdout hijacking** is the central Python-specific concern: a
:class:`_FakeStream` replaces ``sys.stdout`` while the Typer/Click command
tree runs, capturing all non-SDK output (Click error messages, help text,
third-party prints). The real SDK Writer targets the *original* stdout so
JSONL envelopes always reach the consumer uncorrupted.
"""

from __future__ import annotations

import io
import os
import sys
import traceback
from datetime import datetime
from typing import Any, Callable, Dict, Optional, Sequence

from agentsdk.crashdump import CrashDump, write_crash_dump
from agentsdk.exitcode import (
    EXIT_FATAL_ERROR,
    EXIT_SUCCESS,
    ErrorCodeRegistry,
    ExitError,
)
from agentsdk.flightcontext import FlightContext
from agentsdk.sandbox import Sandbox
from agentsdk.signalhandler import SignalHandlerConfig, setup_signal_handler
from agentsdk.writer import Writer


# ---------------------------------------------------------------------------
# _FakeStream — stdout capture buffer
# ---------------------------------------------------------------------------


class _FakeStream:
    """Captures all writes to ``sys.stdout`` during :meth:`App.run`.

    Uses **composition** (wrapping an :class:`io.StringIO` buffer) rather
    than inheriting from :class:`io.TextIOBase`.  This avoids edge cases
    with Python's IO stack that assumes full ``TextIOBase`` compliance.

    After ``run()`` completes, the :attr:`App.captured_output` property
    exposes the captured text for test inspection.
    """

    def __init__(self) -> None:
        self._buffer = io.StringIO()

    def write(self, s: str) -> int:
        """Append *s* to the internal buffer."""
        return self._buffer.write(s)

    def flush(self) -> None:
        """No-op — the buffer is in-memory."""

    def isatty(self) -> bool:
        """Always ``False`` — this is a capture buffer, not a terminal."""
        return False

    def getvalue(self) -> str:
        """Return everything written so far."""
        return self._buffer.getvalue()


# ---------------------------------------------------------------------------
# App — main SDK entry point
# ---------------------------------------------------------------------------


class App:
    """Main SDK entry point that composes all agentsdk components.

    Parameters
    ----------
    name:
        Application name (used in envelopes, crash dumps, and sandbox dir).
    version:
        Application version string.
    """

    def __init__(self, name: str, version: str) -> None:
        self._name = name
        self._version = version
        self._real_stdout = sys.stdout

        # Placeholder writer (run() creates the real one targeting _real_stdout).
        self._writer: Writer = Writer(io.StringIO(), tool_name=name)
        self._registry = ErrorCodeRegistry()
        self._sandbox = Sandbox(name)
        self._flight_context = FlightContext()

        # Agent command registration maps.
        self._config_providers: Dict[str, Any] = {}
        self._health_checks: Dict[str, Callable[..., Any]] = {}
        self._command_meta: Dict[str, Any] = {}

        # Read trace ID from environment.
        self._trace_id: str = os.environ.get("AGENT_TRACE_ID", "")
        if self._trace_id:
            self._writer.set_trace_id(self._trace_id)

        # on_help callback (setup-time, survives reset_for_testing per D024).
        self._on_help_callback: Optional[Callable[[str], None]] = None

        # Last _FakeStream, exposed for test inspection.
        self._fake_stream: Optional[_FakeStream] = None

    # ------------------------------------------------------------------
    # Properties
    # ------------------------------------------------------------------

    @property
    def name(self) -> str:
        """Application name."""
        return self._name

    @property
    def version(self) -> str:
        """Application version."""
        return self._version

    @property
    def writer(self) -> Writer:
        """JSONL Writer for structured output."""
        return self._writer

    @property
    def sandbox(self) -> Sandbox:
        """Sandbox for directory management."""
        return self._sandbox

    @property
    def flight_context(self) -> FlightContext:
        """FlightContext for in-flight state recording."""
        return self._flight_context

    @property
    def captured_output(self) -> str:
        """Return the output captured by the last ``run()`` call's fake stream.

        Returns an empty string if ``run()`` has not been called yet.
        """
        if self._fake_stream is not None:
            return self._fake_stream.getvalue()
        return ""

    # ------------------------------------------------------------------
    # Writer injection (MEM014)
    # ------------------------------------------------------------------

    def set_writer(self, writer: Writer) -> None:
        """Replace the internal writer.

        Primarily useful for injecting a test double before calling
        methods that emit envelopes outside of :meth:`run`.
        """
        self._writer = writer

    # ------------------------------------------------------------------
    # reset_for_testing
    # ------------------------------------------------------------------

    def reset_for_testing(self) -> None:
        """Reset runtime state for test isolation (per D024).

        Resets only **runtime** state that accumulates between ``run()``
        calls:

        - **writer**: replaced with a fresh :class:`Writer` (quiet=False,
          trace_id empty).
        - **flight_context**: all key-value pairs removed via
          :meth:`FlightContext.clear`.
        - **fake_stream**: set to ``None`` so :attr:`captured_output`
          returns ``""``.

        **NOT** reset (these survive across test runs because they are
        registered during import / module setup):

        - ``error_code`` registry (built-in + custom codes)
        - ``config_providers``
        - ``health_checks``
        - ``command_meta``
        - ``on_help`` callback (setup-time hook per D024)
        """
        self._writer = Writer(io.StringIO(), tool_name=self._name)
        self._flight_context.clear()
        self._fake_stream = None

    # ------------------------------------------------------------------
    # Register methods
    # ------------------------------------------------------------------

    def register_error_code(self, code: str, exit_code: int, description: str) -> None:
        """Register a custom error code.  Built-in codes cannot be overridden."""
        self._registry.register(code, exit_code, description)

    def register_config(self, name: str, provider: Any) -> None:
        """Register a named config provider for agent config commands."""
        self._config_providers[name] = provider

    def register_health_check(self, name: str, fn: Callable[..., Any]) -> None:
        """Register a named health check function for agent doctor."""
        self._health_checks[name] = fn

    def register_command_meta(self, cmd_path: str, meta: Any) -> None:
        """Register metadata enrichment for a command path."""
        self._command_meta[cmd_path] = meta

    def on_help(self, callback: Callable[[str], None]) -> None:
        """Register a callback invoked when ``--help`` output is captured.

        The callback receives the captured help text as its sole argument.
        When a callback is registered, the default auto-wrap behaviour
        (emitting a ``kind='help'`` envelope) is suppressed — the callback
        is fully responsible for handling the text.

        This is a **setup-time** registration and survives
        :meth:`reset_for_testing` (per D024).
        """
        self._on_help_callback = callback

    def error_code_to_exit_code(self, code: str) -> int:
        """Look up the numeric exit code for *code*.

        Returns ``EXIT_FATAL_ERROR`` for unknown codes.
        """
        return self._registry.to_exit_code(code)

    def has_error_code(self, code: str) -> bool:
        """Return ``True`` if *code* is registered (built-in or custom)."""
        return self._registry.has_error_code(code)

    # ------------------------------------------------------------------
    # run() — main orchestration
    # ------------------------------------------------------------------

    def run(self, typer_app: Any, args: Optional[Sequence[str]] = None) -> int:
        """Run a Typer CLI app with stdout hijacking and exception recovery.

        1. Replace ``sys.stdout`` with a :class:`_FakeStream`.
        2. Create a real :class:`Writer` targeting the original stdout.
        3. Add ``--quiet`` / ``-q`` flag to the Click command tree.
        4. Start a signal handler for SIGINT / SIGTERM.
        5. Invoke the Typer app with panic recovery.
        6. On success return ``EXIT_SUCCESS``; on :class:`ExitError` return
           its code; on any other exception emit ``FATAL_CRASH`` and return
           ``EXIT_FATAL_ERROR``.
        7. In all cases, restore ``sys.stdout`` and stop the signal handler.

        Parameters
        ----------
        typer_app:
            A ``typer.Typer`` instance.
        args:
            CLI arguments.  ``None`` means *use* ``sys.argv[1:]``.

        Returns
        -------
        int
            Exit code (0–5).
        """
        import click
        import typer

        # -- 1. Stdout hijacking ------------------------------------------------
        fake = _FakeStream()
        self._fake_stream = fake
        sys.stdout = fake

        # -- 2. Real writer targeting original stdout ---------------------------
        real_writer = Writer(self._real_stdout, tool_name=self._name)
        if self._trace_id:
            real_writer.set_trace_id(self._trace_id)
        self._writer = real_writer

        # -- 3. Get Click command & wire --quiet flag ---------------------------
        click_cmd = typer.main.get_command(typer_app)

        has_quiet = any(
            getattr(p, "name", None) == "quiet" for p in (click_cmd.params or [])
        )
        if not has_quiet:
            # Use eager callback with expose_value=False so --quiet is
            # processed during parsing but NOT passed to the command callback.
            # This mirrors Go SDK's PersistentPreRunE pattern.
            def _quiet_callback(
                ctx: click.Context,  # type: ignore[type-arg]
                param: click.Parameter,
                value: bool,
            ) -> None:
                real_writer.set_quiet(value)

            quiet_opt = click.Option(
                ["--quiet", "-q"],
                is_flag=True,
                default=False,
                expose_value=False,
                is_eager=True,
                callback=_quiet_callback,
                help="Suppress progress and warning output",
            )
            click_cmd.params = list(click_cmd.params or []) + [quiet_opt]

        # -- 4. Signal handler --------------------------------------------------
        stop_fn = setup_signal_handler(
            self._name,
            self._version,
            self._trace_id,
            self._sandbox,
            self._flight_context,
            config=SignalHandlerConfig(
                # In production, the signal handler should exit.
                # During run(), SystemExit propagates through the finally block.
                on_signal=lambda: sys.exit(EXIT_FATAL_ERROR),
            ),
        )

        # -- 5-6. Run with panic recovery ---------------------------------------
        code = EXIT_SUCCESS
        help_invoked = "--help" in (args or [])
        try:
            try:
                click_cmd(args, standalone_mode=False)
            except ExitError as exc:
                code = exc.code
            except SystemExit as exc:
                # From signal handler's on_signal or Click internals.
                code = exc.code if isinstance(exc.code, int) else EXIT_SUCCESS
            except BaseException as exc:
                # Panic recovery — capture traceback, write crash dump, emit
                # FATAL_CRASH JSONL envelope.
                tb_str = traceback.format_exc()
                panic_value = str(exc)

                # Best-effort crash dump write.
                try:
                    dump = CrashDump(
                        timestamp=datetime.now().isoformat(),
                        app_name=self._name,
                        app_version=self._version,
                        trace_id=self._trace_id,
                        crash_type="panic",
                        panic_value=panic_value,
                        stack_trace=tb_str,
                        flight_context=self._flight_context.snapshot(),
                    )
                    write_crash_dump(self._sandbox, dump)
                except Exception as dump_err:
                    # Best-effort: log but don't block JSONL emission.
                    print(
                        f"crashdump: panic write failed: {dump_err}",
                        file=self._real_stdout,
                    )

                msg = f"panic: {panic_value}\n{tb_str}"
                real_writer.error_with_code("FATAL_CRASH", msg)
                code = EXIT_FATAL_ERROR

            # -- Help handling (non-crash exit with captured output) --
            # Fires when --help was invoked (Click prints help to stdout in
            # standalone_mode=False without raising SystemExit). Normal command
            # completion that prints to stdout is NOT treated as help output.
            if code == EXIT_SUCCESS and help_invoked:
                captured = fake.getvalue()
                if captured.strip():
                    if self._on_help_callback:
                        self._on_help_callback(captured)
                    else:
                        real_writer.success(captured, kind="help")
        finally:
            # -- 7. Cleanup -----------------------------------------------------
            sys.stdout = self._real_stdout
            stop_fn()

        return code

    # ------------------------------------------------------------------
    # Agent commands
    # ------------------------------------------------------------------

    def agent_commands(self) -> Any:
        """Return a Typer app containing all 6 agent meta-commands.

        The returned Typer app has ``name="agent"`` and includes:
        ``schema``, ``errors``, ``config list``, ``config set``,
        ``doctor``, ``debug-last-crash``, and ``cache-clean``.

        Usage::

            import typer
            from agentsdk import App

            app = App("my-tool", "1.0")
            cli = typer.Typer()
            cli.add_typer(app.agent_commands(), name="agent")
        """
        from agentsdk.agent_commands import create_agent_app

        return create_agent_app(self)
