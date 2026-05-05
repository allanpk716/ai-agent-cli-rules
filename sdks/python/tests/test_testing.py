"""Tests for agentsdk.testing — pytest helpers for JSONL envelope output."""

from __future__ import annotations

import json

import pytest

from agentsdk.app import App
from agentsdk.testing import (
    TestApp,
    capture_output,
    must_parse_envelope,
    must_parse_envelopes,
    parse_envelope,
    parse_envelopes,
)
from agentsdk.writer import Writer


# ============================================================================
# TestApp
# ============================================================================


class TestTestApp:
    """TestApp(name, version) returns (App, StringIO) with writer wired."""

    def test_returns_app_and_buffer(self) -> None:
        app, buf = TestApp()
        assert isinstance(app, App)
        assert hasattr(buf, "getvalue")

    def test_custom_name_and_version(self) -> None:
        app, buf = TestApp("my-cli", "2.5.0")
        assert app.name == "my-cli"
        assert app.version == "2.5.0"

    def test_writer_emits_to_buffer(self) -> None:
        app, buf = TestApp("t", "1.0")
        app.writer.success({"ok": True})
        output = buf.getvalue()
        assert output.strip() != ""
        envelope = json.loads(output)
        assert envelope["type"] == "result"
        assert envelope["tool"] == "t"
        assert envelope["data"] == {"ok": True}

    def test_multiple_envelopes_separated_by_newlines(self) -> None:
        app, buf = TestApp("t", "1.0")
        app.writer.success({"step": 1})
        app.writer.warning("watch out")
        app.writer.progress(50, "halfway")
        lines = buf.getvalue().strip().splitlines()
        assert len(lines) == 3

    def test_default_name_and_version(self) -> None:
        app, _ = TestApp()
        assert app.name == "test-tool"
        assert app.version == "0.0.1"


# ============================================================================
# parse_envelope
# ============================================================================


class TestParseEnvelope:
    """parse_envelope(line) parses a single JSONL line."""

    def test_valid_jsonl(self) -> None:
        line = '{"type":"result","data":42}'
        result = parse_envelope(line)
        assert result["type"] == "result"
        assert result["data"] == 42

    def test_trailing_newline_stripped(self) -> None:
        line = '{"type":"result"}\n'
        result = parse_envelope(line)
        assert result["type"] == "result"

    def test_invalid_json_raises_value_error(self) -> None:
        with pytest.raises(ValueError, match="invalid JSONL line"):
            parse_envelope("not json")

    def test_empty_string_raises_value_error(self) -> None:
        with pytest.raises(ValueError, match="invalid JSONL line"):
            parse_envelope("")

    def test_partial_json_raises_value_error(self) -> None:
        with pytest.raises(ValueError, match="invalid JSONL line"):
            parse_envelope('{"type":')

    def test_preserves_all_fields(self) -> None:
        line = json.dumps({"version": "1.0", "tool": "x", "type": "error",
                           "error_code": "E001", "message": "boom"})
        result = parse_envelope(line)
        assert result["error_code"] == "E001"
        assert result["message"] == "boom"


# ============================================================================
# parse_envelopes
# ============================================================================


class TestParseEnvelopes:
    """parse_envelopes(output) splits multi-line JSONL into dicts."""

    def test_multiple_valid_lines(self) -> None:
        output = '{"type":"result","data":1}\n{"type":"warning","message":"m"}\n'
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 2
        assert envelopes[0]["type"] == "result"
        assert envelopes[1]["type"] == "warning"

    def test_skips_blank_lines(self) -> None:
        output = '{"type":"result"}\n\n{"type":"error"}\n'
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 2

    def test_skips_whitespace_only_lines(self) -> None:
        output = '{"type":"result"}\n   \t  \n{"type":"error"}\n'
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 2

    def test_empty_input_returns_empty_list(self) -> None:
        assert parse_envelopes("") == []

    def test_only_blank_lines_returns_empty_list(self) -> None:
        assert parse_envelopes("\n\n  \n\t\n") == []

    def test_stops_on_first_malformed_line(self) -> None:
        output = '{"type":"result"}\nBROKEN\n{"type":"error"}\n'
        with pytest.raises(ValueError, match="invalid JSONL line"):
            parse_envelopes(output)

    def test_single_line_no_trailing_newline(self) -> None:
        output = '{"type":"result","data":"ok"}'
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 1
        assert envelopes[0]["data"] == "ok"


# ============================================================================
# must_parse_envelope
# ============================================================================


class TestMustParseEnvelope:
    """must_parse_envelope uses pytest.fail on parse errors."""

    def test_valid_line(self) -> None:
        result = must_parse_envelope('{"type":"result"}')
        assert result["type"] == "result"

    def test_invalid_line_fails_test(self) -> None:
        with pytest.raises(pytest.fail.Exception, match="invalid JSONL line"):
            must_parse_envelope("NOT JSON")


# ============================================================================
# must_parse_envelopes
# ============================================================================


class TestMustParseEnvelopes:
    """must_parse_envelopes uses pytest.fail on parse errors."""

    def test_valid_multi_line(self) -> None:
        output = '{"type":"result"}\n{"type":"warning","message":"m"}\n'
        envelopes = must_parse_envelopes(output)
        assert len(envelopes) == 2

    def test_invalid_line_fails_test(self) -> None:
        with pytest.raises(pytest.fail.Exception, match="invalid JSONL line"):
            must_parse_envelopes('{"type":"result"}\nBROKEN\n')

    def test_empty_input(self) -> None:
        envelopes = must_parse_envelopes("")
        assert envelopes == []


# ============================================================================
# capture_output
# ============================================================================


class TestCaptureOutput:
    """capture_output temporarily rewires writer and returns JSONL."""

    def test_captures_success_envelope(self) -> None:
        app, _ = TestApp("cap", "1.0")

        def do_work() -> None:
            app.writer.success({"captured": True})

        output = capture_output(app, do_work)
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 1
        assert envelopes[0]["data"] == {"captured": True}

    def test_captures_multiple_envelopes(self) -> None:
        app, _ = TestApp("cap", "1.0")

        def do_work() -> None:
            app.writer.success({"step": 1})
            app.writer.warning("careful")
            app.writer.error("boom")

        output = capture_output(app, do_work)
        envelopes = parse_envelopes(output)
        assert len(envelopes) == 3

    def test_restores_original_writer(self) -> None:
        app, buf = TestApp("cap", "1.0")
        original = app.writer

        capture_output(app, lambda: app.writer.success({"x": 1}))
        assert app.writer is original

    def test_restores_writer_on_exception(self) -> None:
        app, buf = TestApp("cap", "1.0")
        original = app.writer

        def boom() -> None:
            app.writer.success({"before": True})
            raise RuntimeError("kaboom")

        with pytest.raises(RuntimeError, match="kaboom"):
            capture_output(app, boom)

        # Writer must be restored even after exception.
        assert app.writer is original

    def test_passes_args_and_kwargs(self) -> None:
        app, _ = TestApp("cap", "1.0")

        def do_work(a: int, b: str, extra: str = "") -> None:
            app.writer.success({"a": a, "b": b, "extra": extra})

        output = capture_output(app, do_work, 42, "hello", extra="world")
        envelope = parse_envelope(output)
        assert envelope["data"] == {"a": 42, "b": "hello", "extra": "world"}

    def test_empty_capture_when_no_output(self) -> None:
        app, _ = TestApp("cap", "1.0")
        output = capture_output(app, lambda: None)
        assert output == ""

    def test_capture_isolates_from_external_buffer(self) -> None:
        """Envelopes written during capture_output don't leak to external buffer."""
        app, buf = TestApp("cap", "1.0")

        def do_work() -> None:
            app.writer.success({"inside": True})

        output = capture_output(app, do_work)
        # External buffer should still be empty — capture used its own.
        assert buf.getvalue() == ""
        # The captured output should contain the envelope.
        assert "inside" in output
