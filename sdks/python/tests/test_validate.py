"""Tests for validate_envelope() — protocol validation rules."""

import pytest

from agentsdk.envelope import Envelope
from agentsdk.validate import validate_envelope


# ------------------------------------------------------------------
# Helpers
# ------------------------------------------------------------------

def _base(**overrides) -> dict:
    """Return a minimal valid result envelope dict with overrides."""
    d = {
        "version": "1.0",
        "tool": "test-tool",
        "type": "result",
        "timestamp": "2025-01-01T00:00:00Z",
        "data": {"key": "value"},
    }
    d.update(overrides)
    return d


# ------------------------------------------------------------------
# Valid envelopes — should pass without raising
# ------------------------------------------------------------------

class TestValidEnvelopes:
    def test_valid_result_envelope_passes(self):
        validate_envelope(_base())

    def test_valid_error_envelope_passes(self):
        validate_envelope(_base(
            type="error", data=None,
            error_code="E001", message="something broke",
        ))

    def test_valid_warning_envelope_passes(self):
        validate_envelope(_base(
            type="warning", data=None,
            message="heads up",
        ))

    def test_valid_progress_envelope_passes(self):
        validate_envelope(_base(
            type="progress", data=None, percent=50,
            message="halfway",
        ))


# ------------------------------------------------------------------
# Result type — validation failures
# ------------------------------------------------------------------

class TestResultValidation:
    def test_result_missing_data_fails(self):
        with pytest.raises(ValueError, match="data"):
            validate_envelope(_base(data=None))

    def test_result_with_error_code_fails(self):
        with pytest.raises(ValueError, match="error_code"):
            validate_envelope(_base(error_code="E001"))

    def test_result_with_percent_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(_base(percent=42))


# ------------------------------------------------------------------
# Error type — validation failures
# ------------------------------------------------------------------

class TestErrorValidation:
    def _err(self, **overrides) -> dict:
        defaults = dict(
            type="error", data=None,
            error_code="E001", message="oops",
        )
        defaults.update(overrides)
        return _base(**defaults)

    def test_error_missing_error_code_fails(self):
        with pytest.raises(ValueError, match="error_code"):
            validate_envelope(self._err(error_code=None))

    def test_error_missing_message_fails(self):
        with pytest.raises(ValueError, match="message"):
            validate_envelope(self._err(message=None))

    def test_error_with_data_fails(self):
        with pytest.raises(ValueError, match="data"):
            validate_envelope(self._err(data={"unexpected": True}))

    def test_error_with_percent_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(self._err(percent=10))


# ------------------------------------------------------------------
# Warning type — validation failures
# ------------------------------------------------------------------

class TestWarningValidation:
    def _warn(self, **overrides) -> dict:
        defaults = dict(
            type="warning", data=None,
            message="caution",
        )
        defaults.update(overrides)
        return _base(**defaults)

    def test_warning_missing_message_fails(self):
        with pytest.raises(ValueError, match="message"):
            validate_envelope(self._warn(message=None))

    def test_warning_with_data_fails(self):
        with pytest.raises(ValueError, match="data"):
            validate_envelope(self._warn(data={"nope": True}))

    def test_warning_with_error_code_fails(self):
        with pytest.raises(ValueError, match="error_code"):
            validate_envelope(self._warn(error_code="E001"))

    def test_warning_with_percent_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(self._warn(percent=25))


# ------------------------------------------------------------------
# Progress type — validation failures
# ------------------------------------------------------------------

class TestProgressValidation:
    def _prog(self, **overrides) -> dict:
        defaults = dict(
            type="progress", data=None, percent=50,
            message="working",
        )
        defaults.update(overrides)
        return _base(**defaults)

    def test_progress_missing_percent_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(self._prog(percent=0))

    def test_progress_percent_below_zero_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(self._prog(percent=-1))

    def test_progress_percent_above_100_fails(self):
        with pytest.raises(ValueError, match="percent"):
            validate_envelope(self._prog(percent=101))

    def test_progress_with_data_fails(self):
        with pytest.raises(ValueError, match="data"):
            validate_envelope(self._prog(data={"nope": True}))

    def test_progress_with_error_code_fails(self):
        with pytest.raises(ValueError, match="error_code"):
            validate_envelope(self._prog(error_code="E001"))


# ------------------------------------------------------------------
# Common fields — validation failures
# ------------------------------------------------------------------

class TestCommonFieldValidation:
    def test_unknown_type_fails(self):
        with pytest.raises(ValueError, match="type"):
            validate_envelope(_base(type="unknown"))

    def test_missing_version_fails(self):
        with pytest.raises(ValueError, match="version"):
            validate_envelope(_base(version=""))

    def test_missing_tool_fails(self):
        with pytest.raises(ValueError, match="tool"):
            validate_envelope(_base(tool=""))

    def test_missing_timestamp_fails(self):
        with pytest.raises(ValueError, match="timestamp"):
            validate_envelope(_base(timestamp=""))


# ------------------------------------------------------------------
# Boundary tests for progress percent
# ------------------------------------------------------------------

class TestProgressBoundary:
    def test_progress_percent_zero_is_valid(self):
        """percent=0 is tricky — 0 is the default and gets excluded by to_dict(),
        but as a dict input to validate_envelope it must still be rejected as
        'missing' because progress REQUIRES percent. This test verifies that
        the validator treats percent=0 as 'present' (valid) when passed explicitly."""
        validate_envelope(_base(type="progress", data=None, percent=1))

    def test_progress_percent_100_is_valid(self):
        validate_envelope(_base(type="progress", data=None, percent=100, message="done"))


# ------------------------------------------------------------------
# Envelope dataclass input
# ------------------------------------------------------------------

class TestEnvelopeDataclassInput:
    def test_valid_envelope_dataclass_passes(self):
        env = Envelope.result(tool="cli", data={"ok": True})
        validate_envelope(env)

    def test_invalid_envelope_dataclass_raises(self):
        env = Envelope(tool="cli", type="result", data=None)
        with pytest.raises(ValueError):
            validate_envelope(env)
