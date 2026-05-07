"""Tests for agentsdk.writer — Writer with quiet filter and trace ID injection."""

from __future__ import annotations

import io
import json

from agentsdk.writer import Writer


def _parse_jsonl(output: str) -> list[dict]:
    """Parse all JSONL lines from a string into a list of dicts."""
    lines = [l for l in output.strip().splitlines() if l.strip()]
    return [json.loads(line) for line in lines]


class TestSuccess:
    def test_success_emits_result_envelope(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"key": "value"})
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["type"] == "result"
        assert lines[0]["data"] == {"key": "value"}
        assert lines[0]["tool"] == "mytool"


class TestError:
    def test_error_with_code_emits_error_envelope(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.error_with_code("not_found", "thing missing")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["type"] == "error"
        assert lines[0]["error_code"] == "not_found"
        assert lines[0]["message"] == "thing missing"

    def test_error_default_code_is_error_string(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.error("something broke")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["type"] == "error"
        assert lines[0]["error_code"] == "error"
        assert lines[0]["message"] == "something broke"


class TestWarning:
    def test_warning_emits_warning_envelope(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.warning("disk almost full")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["type"] == "warning"
        assert lines[0]["message"] == "disk almost full"


class TestProgress:
    def test_progress_emits_progress_envelope(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.progress(50, "halfway")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["type"] == "progress"
        assert lines[0]["percent"] == 50
        assert lines[0]["message"] == "halfway"


class TestQuiet:
    def test_quiet_filters_progress_and_warning(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.set_quiet(True)
        assert w.progress(10, "loading") is None
        assert w.warning("deprecated") is None
        assert buf.getvalue() == ""

    def test_quiet_does_not_filter_result_and_error(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.set_quiet(True)
        w.success({"ok": True})
        w.error("still report errors")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 2
        assert lines[0]["type"] == "result"
        assert lines[1]["type"] == "error"


class TestTraceId:
    def test_trace_id_injected_when_set(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.set_trace_id("abc-123")
        w.success({"done": True})
        lines = _parse_jsonl(buf.getvalue())
        assert lines[0]["trace_id"] == "abc-123"

    def test_trace_id_omitted_when_empty(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"done": True})
        lines = _parse_jsonl(buf.getvalue())
        assert "trace_id" not in lines[0]

    def test_trace_id_change_mid_stream(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"step": 1})
        w.set_trace_id("trace-xyz")
        w.success({"step": 2})
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 2
        assert "trace_id" not in lines[0]
        assert lines[1]["trace_id"] == "trace-xyz"

    def test_trace_id_property(self):
        w = Writer(io.StringIO(), tool_name="t")
        assert w.trace_id == ""
        w.set_trace_id("id-1")
        assert w.trace_id == "id-1"


class TestJsonlFormat:
    def test_output_is_valid_jsonl(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"a": 1})
        w.warning("watch out")
        w.error_with_code("fail", "boom")
        w.progress(100, "done")
        raw = buf.getvalue()
        # Each line must be independently parseable JSON
        for line in raw.strip().splitlines():
            obj = json.loads(line)
            assert "type" in obj
            assert "tool" in obj
            assert "timestamp" in obj
            assert "version" in obj


class TestKind:
    def test_success_with_kind(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"k": 1}, kind="schema")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["kind"] == "schema"

    def test_success_without_kind(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"k": 1})
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert "kind" not in lines[0]

    def test_success_with_empty_kind(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.success({"k": 1}, kind="")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert "kind" not in lines[0]

    def test_kind_preserved_with_trace_id(self):
        buf = io.StringIO()
        w = Writer(buf, tool_name="mytool")
        w.set_trace_id("trace-abc")
        w.success({"k": 1}, kind="schema")
        lines = _parse_jsonl(buf.getvalue())
        assert len(lines) == 1
        assert lines[0]["kind"] == "schema"
        assert lines[0]["trace_id"] == "trace-abc"
