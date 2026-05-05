"""agentsdk.testing — pytest helpers for verifying JSONL envelope output.

Provides :class:`TestApp`, parsing utilities, and :func:`capture_output`
for zero-friction testing of agentsdk-based CLIs.  Users import directly::

    from agentsdk.testing import TestApp, parse_envelopes, capture_output
"""

from __future__ import annotations

import io
import json
from typing import Any, Callable, Tuple

from agentsdk.app import App
from agentsdk.writer import Writer

__all__ = [
    "TestApp",
    "parse_envelope",
    "parse_envelopes",
    "must_parse_envelope",
    "must_parse_envelopes",
    "capture_output",
]


# ---------------------------------------------------------------------------
# TestApp
# ---------------------------------------------------------------------------


def TestApp(name: str = "test-tool", version: str = "0.0.1") -> Tuple[App, io.StringIO]:
    """Create an :class:`~agentsdk.App` wired to a :class:`io.StringIO` buffer.

    Returns a ``(app, buffer)`` tuple.  Anything the app's writer emits
    (via ``app.writer.success()``, ``app.writer.error()``, etc.) is
    captured in *buffer* as JSONL lines.

    This is the Python equivalent of the Go SDK's ``NewTestApp`` helper.

    Parameters
    ----------
    name:
        Application name (written into every envelope's ``tool`` field).
    version:
        Application version string.

    Returns
    -------
    tuple[App, io.StringIO]
        A ready-to-use ``(app, buffer)`` pair.

    Example
    -------
    >>> from agentsdk.testing import TestApp
    >>> app, buf = TestApp("my-tool", "1.0")
    >>> app.writer.success({"count": 42})
    >>> import json
    >>> envelope = json.loads(buf.getvalue())
    >>> envelope["type"]
    'result'
    """
    buf = io.StringIO()
    app = App(name, version)
    app.set_writer(Writer(buf, tool_name=name))
    return app, buf


# ---------------------------------------------------------------------------
# Parsing helpers
# ---------------------------------------------------------------------------


def parse_envelope(line: str) -> dict[str, Any]:
    """Parse a single JSONL line into a dict.

    Parameters
    ----------
    line:
        One JSONL line (may include trailing newline).

    Returns
    -------
    dict[str, Any]
        The parsed envelope as a plain dictionary.

    Raises
    ------
    ValueError
        If *line* is not valid JSON.
    """
    stripped = line.strip()
    try:
        return json.loads(stripped)
    except json.JSONDecodeError as exc:
        raise ValueError(f"invalid JSONL line: {exc}") from exc


def parse_envelopes(output: str) -> list[dict[str, Any]]:
    """Parse multi-line JSONL output into a list of dicts.

    Blank lines and whitespace-only lines are silently skipped.
    Parsing stops at the first malformed line.

    Parameters
    ----------
    output:
        Multi-line JSONL string.

    Returns
    -------
    list[dict[str, Any]]
        One dict per non-blank line.

    Raises
    ------
    ValueError
        On the first line that fails JSON parsing.
    """
    envelopes: list[dict[str, Any]] = []
    for line in output.splitlines():
        if not line.strip():
            continue
        envelopes.append(parse_envelope(line))
    return envelopes


def must_parse_envelope(output: str) -> dict[str, Any]:
    """Parse a single JSONL envelope, calling ``pytest.fail`` on error.

    Convenience wrapper around :func:`parse_envelope` for use inside
    test functions where a nice assertion message is preferred over
    a raw ``ValueError`` traceback.

    Parameters
    ----------
    output:
        One JSONL line.

    Returns
    -------
    dict[str, Any]
        The parsed envelope dict.

    Raises
    ------
    pytest.fail.Exception
        If the line cannot be parsed as JSON.
    """
    try:
        return parse_envelope(output)
    except ValueError as exc:
        import pytest

        pytest.fail(str(exc))


def must_parse_envelopes(output: str) -> list[dict[str, Any]]:
    """Parse multi-line JSONL output, calling ``pytest.fail`` on error.

    Convenience wrapper around :func:`parse_envelopes` for use inside
    test functions.

    Parameters
    ----------
    output:
        Multi-line JSONL string.

    Returns
    -------
    list[dict[str, Any]]
        Parsed envelope dicts.

    Raises
    ------
    pytest.fail.Exception
        If any non-blank line cannot be parsed as JSON.
    """
    try:
        return parse_envelopes(output)
    except ValueError as exc:
        import pytest

        pytest.fail(str(exc))


# ---------------------------------------------------------------------------
# capture_output
# ---------------------------------------------------------------------------


def capture_output(app: App, fn: Callable[..., Any], *args: Any, **kwargs: Any) -> str:
    """Temporarily rewire *app*'s writer to a fresh buffer and capture output.

    The original writer is saved before *fn* runs and restored afterwards
    (even if *fn* raises).  This is safe to nest — each call creates its
    own buffer and restores the previous writer on exit.

    Parameters
    ----------
    app:
        The :class:`~agentsdk.App` whose writer should be captured.
    fn:
        A callable that writes envelopes via ``app.writer``.
    *args, **kwargs:
        Forwarded to *fn*.

    Returns
    -------
    str
        All JSONL lines emitted during *fn*'s execution.
    """
    original_writer = app.writer
    buf = io.StringIO()
    app.set_writer(Writer(buf, tool_name=app.name))
    try:
        fn(*args, **kwargs)
    finally:
        app.set_writer(original_writer)
    return buf.getvalue()
