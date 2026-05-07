"""Tests for ExitCode constants, ExitError, and ErrorCodeRegistry."""

import pytest

from agentsdk.exitcode import (
    EXIT_SUCCESS,
    EXIT_FATAL_ERROR,
    EXIT_INVALID_PARAMS,
    EXIT_NOT_FOUND,
    EXIT_NETWORK_ERROR,
    EXIT_LOCK_CONFLICT,
    ExitError,
    ErrorCodeRegistry,
)


class TestExitCodeConstants:
    """Verify the 6 exit code constants match Go SDK values (0-5)."""

    def test_exit_code_constants(self):
        assert EXIT_SUCCESS == 0
        assert EXIT_FATAL_ERROR == 1
        assert EXIT_INVALID_PARAMS == 2
        assert EXIT_NOT_FOUND == 3
        assert EXIT_NETWORK_ERROR == 4
        assert EXIT_LOCK_CONFLICT == 5


class TestExitError:
    """Verify ExitError exception behaviour."""

    def test_exit_error_message_format(self):
        err = ExitError(code=2, message="bad input")
        assert str(err) == "exit 2: bad input"

    def test_exit_error_with_none_err(self):
        err = ExitError(code=1, message="crash", original_error=None)
        assert str(err) == "exit 1: crash"
        assert err.original_error is None

    def test_exit_error_wraps_original_exception(self):
        original = ValueError("bad value")
        err = ExitError(code=2, message="validation failed", original_error=original)
        assert str(err) == "exit 2: validation failed"
        assert err.original_error is original
        assert isinstance(err.original_error, ValueError)


class TestErrorCodeRegistry:
    """Verify ErrorCodeRegistry with built-in code protection."""

    BUILTIN_CODES = ["FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"]

    def test_registry_builtin_codes_exist(self):
        reg = ErrorCodeRegistry()
        for code in self.BUILTIN_CODES:
            exit_code, description, found = reg.lookup(code)
            assert found is True, f"Built-in code {code} should be found"
            assert isinstance(exit_code, int)
            assert isinstance(description, str) and len(description) > 0

    def test_registry_builtin_codes_cannot_be_overridden(self):
        reg = ErrorCodeRegistry()
        for code in self.BUILTIN_CODES:
            with pytest.raises(ValueError, match="(?i)cannot override built-in"):
                reg.register(code, 99, "hacked")

    def test_register_custom_code(self):
        reg = ErrorCodeRegistry()
        reg.register("CUSTOM_TIMEOUT", 10, "request timed out")
        exit_code, desc, found = reg.lookup("CUSTOM_TIMEOUT")
        assert found is True
        assert exit_code == 10
        assert desc == "request timed out"

    def test_reregister_custom_code_updates(self):
        reg = ErrorCodeRegistry()
        reg.register("MY_ERROR", 20, "first version")
        reg.register("MY_ERROR", 21, "second version")
        exit_code, desc, found = reg.lookup("MY_ERROR")
        assert found is True
        assert exit_code == 21
        assert desc == "second version"

    def test_lookup_unknown_returns_fallback_exit_1(self):
        reg = ErrorCodeRegistry()
        exit_code, desc, found = reg.lookup("TOTALLY_UNKNOWN")
        assert found is False
        assert exit_code == EXIT_FATAL_ERROR  # 1
        assert desc == ""

    def test_to_exit_code_unknown_returns_1(self):
        reg = ErrorCodeRegistry()
        assert reg.to_exit_code("NOPE") == EXIT_FATAL_ERROR

    def test_to_exit_code_known(self):
        reg = ErrorCodeRegistry()
        assert reg.to_exit_code("FATAL_CRASH") == EXIT_FATAL_ERROR
        assert reg.to_exit_code("INPUT_INVALID") == EXIT_INVALID_PARAMS

    def test_all_codes_returns_copy(self):
        reg = ErrorCodeRegistry()
        codes = reg.all_codes()
        # Mutation of returned dict should not affect registry
        codes["INJECTED"] = (99, "fake", True)
        exit_code, desc, found = reg.lookup("INJECTED")
        assert found is False


class TestHasErrorCode:
    """Verify has_error_code convenience method on ErrorCodeRegistry."""

    def test_builtin_code_returns_true(self):
        reg = ErrorCodeRegistry()
        assert reg.has_error_code("FATAL_CRASH") is True

    def test_unknown_code_returns_false(self):
        reg = ErrorCodeRegistry()
        assert reg.has_error_code("CUSTOM") is False

    def test_after_register_returns_true(self):
        reg = ErrorCodeRegistry()
        assert reg.has_error_code("CUSTOM") is False
        reg.register("CUSTOM", 10, "custom error")
        assert reg.has_error_code("CUSTOM") is True

    def test_all_builtin_codes_are_found(self):
        reg = ErrorCodeRegistry()
        for code in ["FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"]:
            assert reg.has_error_code(code) is True

    def test_all_codes_returns_non_empty_dict_with_builtin_keys(self):
        reg = ErrorCodeRegistry()
        codes = reg.all_codes()
        assert isinstance(codes, dict)
        assert len(codes) >= 5
        for code in ["FATAL_CRASH", "INTERNAL_ERROR", "INPUT_INVALID", "NOT_FOUND", "RESOURCE_LOCKED"]:
            assert code in codes
