"""Tests for agentsdk.envelope — TDD RED phase.

Covers: constructor field exclusion, JSON serialization, timestamp format,
        version constant, tool name propagation.
"""

import json
from datetime import datetime, timezone

from agentsdk.envelope import (
    ENVELOPE_VERSION,
    TYPE_ERROR,
    TYPE_PROGRESS,
    TYPE_RESULT,
    TYPE_WARNING,
    Envelope,
)


# ---------------------------------------------------------------------------
# Constant tests
# ---------------------------------------------------------------------------

class TestConstants:
    def test_envelope_version_is_1_0(self):
        assert ENVELOPE_VERSION == "1.0"

    def test_type_constants(self):
        assert TYPE_RESULT == "result"
        assert TYPE_ERROR == "error"
        assert TYPE_WARNING == "warning"
        assert TYPE_PROGRESS == "progress"


# ---------------------------------------------------------------------------
# Constructor + field-exclusion tests
# ---------------------------------------------------------------------------

class TestResultEnvelope:
    def test_result_envelope_has_data_only(self):
        env = Envelope.result(tool="my-tool", data={"key": "value"})
        assert env.version == ENVELOPE_VERSION
        assert env.tool == "my-tool"
        assert env.type == TYPE_RESULT
        assert env.data == {"key": "value"}
        # Excluded fields must be zero-valued
        assert env.error_code is None
        assert env.percent == 0

    def test_result_envelope_json_field_exclusion(self):
        env = Envelope.result(tool="t", data={"x": 1})
        raw = env.to_json()
        obj = json.loads(raw)
        assert "data" in obj
        assert "error_code" not in obj
        assert "percent" not in obj


class TestErrorEnvelope:
    def test_error_envelope_has_error_code_and_message(self):
        env = Envelope.error(tool="t", error_code="NETWORK_TIMEOUT", message="conn failed")
        assert env.type == TYPE_ERROR
        assert env.error_code == "NETWORK_TIMEOUT"
        assert env.message == "conn failed"
        # Excluded fields must be zero-valued
        assert env.data is None
        assert env.percent == 0

    def test_error_envelope_json_field_exclusion(self):
        env = Envelope.error(tool="t", error_code="CODE", message="msg")
        raw = env.to_json()
        obj = json.loads(raw)
        assert "error_code" in obj
        assert "message" in obj
        assert "data" not in obj
        assert "percent" not in obj


class TestWarningEnvelope:
    def test_warning_envelope_has_message_only(self):
        env = Envelope.warning(tool="t", message="deprecated API")
        assert env.type == TYPE_WARNING
        assert env.message == "deprecated API"
        # Excluded fields must be zero-valued
        assert env.data is None
        assert env.error_code is None
        assert env.percent == 0

    def test_warning_envelope_json_field_exclusion(self):
        env = Envelope.warning(tool="t", message="msg")
        raw = env.to_json()
        obj = json.loads(raw)
        assert "message" in obj
        assert "data" not in obj
        assert "error_code" not in obj
        assert "percent" not in obj


class TestProgressEnvelope:
    def test_progress_envelope_has_percent_and_message(self):
        env = Envelope.progress(tool="t", percent=75, message="processing...")
        assert env.type == TYPE_PROGRESS
        assert env.percent == 75
        assert env.message == "processing..."
        # Excluded fields must be zero-valued
        assert env.data is None
        assert env.error_code is None

    def test_progress_envelope_json_field_exclusion(self):
        env = Envelope.progress(tool="t", percent=50, message="msg")
        raw = env.to_json()
        obj = json.loads(raw)
        assert "percent" in obj
        assert "message" in obj
        assert "data" not in obj
        assert "error_code" not in obj


# ---------------------------------------------------------------------------
# Cross-cutting: JSON exclusion round-trip, timestamp, version, tool name
# ---------------------------------------------------------------------------

class TestEnvelopeJSONExclusion:
    def test_envelope_json_excludes_zero_fields(self):
        """JSON round-trip proves omitted fields for all four constructor types."""
        envelopes = [
            Envelope.result(tool="t", data={"k": "v"}),
            Envelope.error(tool="t", error_code="CODE", message="msg"),
            Envelope.warning(tool="t", message="msg"),
            Envelope.progress(tool="t", percent=42, message="msg"),
        ]
        for env in envelopes:
            raw = env.to_json()
            obj = json.loads(raw)
            # Always-present fields
            assert "version" in obj
            assert "tool" in obj
            assert "type" in obj
            assert "timestamp" in obj
            # trace_id should be absent when not set
            assert "trace_id" not in obj


class TestEnvelopeTimestamp:
    def test_envelope_timestamp_is_utc_rfc3339(self):
        env = Envelope.result(tool="t", data="x")
        ts = env.timestamp
        # Should parse as RFC3339
        dt = datetime.fromisoformat(ts.replace("Z", "+00:00"))
        assert dt.tzinfo is not None
        # Should be close to now (within 5 seconds)
        now = datetime.now(timezone.utc)
        delta = abs((now - dt).total_seconds())
        assert delta < 5, f"Timestamp {ts} too far from now"


class TestEnvelopeVersion:
    def test_envelope_version_is_1_0(self):
        env = Envelope.result(tool="t", data="x")
        raw = env.to_json()
        obj = json.loads(raw)
        assert obj["version"] == "1.0"


class TestEnvelopeToolName:
    def test_envelope_tool_name_set_correctly(self):
        env = Envelope.result(tool="my-special-tool", data=None)
        raw = env.to_json()
        obj = json.loads(raw)
        assert obj["tool"] == "my-special-tool"


class TestEnvelopeKind:
    def test_result_envelope_with_kind(self):
        env = Envelope.result(tool="t", data="d", kind="help")
        assert env.kind == "help"
        raw = env.to_json()
        obj = json.loads(raw)
        assert obj["kind"] == "help"

    def test_result_envelope_without_kind(self):
        env = Envelope.result(tool="t", data="d")
        assert env.kind is None
        raw = env.to_json()
        obj = json.loads(raw)
        assert "kind" not in obj

    def test_result_envelope_empty_kind(self):
        env = Envelope.result(tool="t", data="d", kind="")
        assert env.kind is None
        raw = env.to_json()
        obj = json.loads(raw)
        assert "kind" not in obj
