"""Writer — the single JSONL output bottleneck.

Composes envelope constructors, quiet filtering, and trace ID injection
into a simple API: ``success()``, ``error()``, ``error_with_code()``,
``warning()``, ``progress()``.
"""

from __future__ import annotations

import json
from typing import IO, Optional

from agentsdk.envelope import Envelope


class Writer:
    """Write structured envelopes to a text stream as JSONL.

    Parameters
    ----------
    output:
        Any text-mode ``IO[str]`` (e.g. ``sys.stdout``, ``io.StringIO``).
    tool_name:
        Tool identifier embedded in every envelope.
    """

    def __init__(self, output: IO[str], *, tool_name: str) -> None:
        self._output = output
        self._tool_name = tool_name
        self._quiet: bool = False
        self._trace_id: str = ""

    # ------------------------------------------------------------------
    # Controls
    # ------------------------------------------------------------------

    def set_quiet(self, quiet: bool) -> None:
        """Enable or disable quiet mode.

        When quiet, ``warning()`` and ``progress()`` are silently
        suppressed — no output, no error raised.
        """
        self._quiet = quiet

    def set_trace_id(self, trace_id: str) -> None:
        """Set a trace ID injected into every subsequent envelope."""
        self._trace_id = trace_id

    @property
    def trace_id(self) -> str:
        """Current trace ID (empty string when unset)."""
        return self._trace_id

    # ------------------------------------------------------------------
    # Public emit methods
    # ------------------------------------------------------------------

    def success(self, data) -> None:
        """Emit a **result** envelope carrying *data*."""
        self._emit(Envelope.result(tool=self._tool_name, data=data))

    def error_with_code(self, code: str, message: str) -> None:
        """Emit an **error** envelope carrying *code* and *message*."""
        self._emit(Envelope.error(tool=self._tool_name, error_code=code, message=message))

    def error(self, message: str) -> None:
        """Emit an **error** envelope with the default code ``"error"``."""
        self.error_with_code("error", message)

    def warning(self, message: str) -> Optional[None]:
        """Emit a **warning** envelope. Silently suppressed in quiet mode."""
        if self._quiet:
            return None
        self._emit(Envelope.warning(tool=self._tool_name, message=message))
        return None

    def progress(self, percent: int, message: str) -> Optional[None]:
        """Emit a **progress** envelope. Silently suppressed in quiet mode."""
        if self._quiet:
            return None
        self._emit(Envelope.progress(tool=self._tool_name, percent=percent, message=message))
        return None

    # ------------------------------------------------------------------
    # Internal bottleneck
    # ------------------------------------------------------------------

    def _emit(self, envelope: Envelope) -> None:
        """Inject trace ID (if set), serialize, and write one JSONL line."""
        if self._trace_id:
            envelope.trace_id = self._trace_id
        line = envelope.to_json()
        self._output.write(line + "\n")
