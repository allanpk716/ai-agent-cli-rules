"""Tests for agentsdk.sandbox — Sandbox directory manager.

Covers: default base dir, env var override, hyphen-to-underscore env var
        construction, subdir accessors, dirs() map, ensure() creation,
        ensure() idempotency, and ensure() error on blocked path.
"""

import os
from pathlib import Path

import pytest

from agentsdk.sandbox import (
    SUBDIR_CACHE,
    SUBDIR_CRASH_DUMPS,
    SUBDIR_DATA,
    SUBDIR_LOCKS,
    Sandbox,
)


# ---------------------------------------------------------------------------
# Helper: isolate env vars per test
# ---------------------------------------------------------------------------

@pytest.fixture(autouse=True)
def _clean_env(monkeypatch):
    """Remove any *_HOME overrides that might leak between tests."""
    for key in list(os.environ):
        if key.endswith("_HOME"):
            monkeypatch.delenv(key, raising=False)


# ---------------------------------------------------------------------------
# Default base dir
# ---------------------------------------------------------------------------

class TestSandboxDefaultBaseDir:
    def test_default_base_dir(self):
        sb = Sandbox("test-tool")
        home = str(Path.home())
        expected = str(Path(home) / ".test-tool")
        assert sb.base_dir == expected


# ---------------------------------------------------------------------------
# Env var override
# ---------------------------------------------------------------------------

class TestSandboxEnvVarOverride:
    def test_env_var_override(self, monkeypatch):
        custom = "/tmp/custom-test-tool-home"
        monkeypatch.setenv("TEST_TOOL_HOME", custom)
        sb = Sandbox("test-tool")
        assert sb.base_dir == custom

    def test_env_var_empty_string_ignored(self, monkeypatch):
        """Empty env var should fall back to default."""
        monkeypatch.setenv("TEST_TOOL_HOME", "")
        sb = Sandbox("test-tool")
        home = str(Path.home())
        expected = str(Path(home) / ".test-tool")
        assert sb.base_dir == expected

    def test_env_var_name_hyphens_to_underscores(self, monkeypatch):
        """App name 'my-cool-agent' should look for MY_COOL_AGENT_HOME."""
        custom = "/tmp/my-cool-agent-home"
        monkeypatch.setenv("MY_COOL_AGENT_HOME", custom)
        sb = Sandbox("my-cool-agent")
        assert sb.base_dir == custom


# ---------------------------------------------------------------------------
# Subdir accessors
# ---------------------------------------------------------------------------

class TestSandboxSubdirPaths:
    def test_data_dir(self):
        sb = Sandbox("test-tool")
        assert sb.data_dir == str(Path(sb.base_dir) / "data")

    def test_cache_dir(self):
        sb = Sandbox("test-tool")
        assert sb.cache_dir == str(Path(sb.base_dir) / "cache")

    def test_locks_dir(self):
        sb = Sandbox("test-tool")
        assert sb.locks_dir == str(Path(sb.base_dir) / "locks")

    def test_crash_dumps_dir(self):
        sb = Sandbox("test-tool")
        assert sb.crash_dumps_dir == str(Path(sb.base_dir) / "crash_dumps")


# ---------------------------------------------------------------------------
# Dirs() map
# ---------------------------------------------------------------------------

class TestSandboxDirs:
    def test_dirs_returns_all_four_subdirs(self):
        sb = Sandbox("test-tool")
        dirs = sb.dirs()
        assert set(dirs.keys()) == {"data", "cache", "locks", "crash_dumps"}

    def test_dirs_values_are_correct_paths(self):
        sb = Sandbox("test-tool")
        dirs = sb.dirs()
        for name, path in dirs.items():
            assert path == str(Path(sb.base_dir) / name)


# ---------------------------------------------------------------------------
# Ensure() — directory creation
# ---------------------------------------------------------------------------

class TestSandboxEnsure:
    def test_ensure_creates_all_dirs(self, tmp_path, monkeypatch):
        monkeypatch.setenv("ENSURE_TEST_HOME", str(tmp_path))
        sb = Sandbox("ensure-test")
        sb.ensure()

        assert Path(sb.base_dir).is_dir()
        for name, path in sb.dirs().items():
            assert Path(path).is_dir(), f"subdir {name!r} was not created"

    def test_ensure_idempotent(self, tmp_path, monkeypatch):
        monkeypatch.setenv("IDEMP_TEST_HOME", str(tmp_path))
        sb = Sandbox("idemp-test")

        sb.ensure()
        sb.ensure()  # second call should succeed

        for name, path in sb.dirs().items():
            assert Path(path).is_dir(), f"subdir {name!r} missing after idempotent ensure"

    def test_ensure_error_file_blocking_directory(self, tmp_path):
        """A file where a directory is expected causes OSError.

        Uses the file-blocking-directory approach (MEM018) instead of
        chmod, which is unreliable on Windows.
        """
        blocking_file = tmp_path / "blocking-file"
        blocking_file.write_text("block")

        sb = Sandbox.__new__(Sandbox)
        sb._app_name = "error-test"
        sb._base_dir = str(blocking_file / "subdir")
        sb._subdirs = [SUBDIR_DATA, SUBDIR_CACHE, SUBDIR_LOCKS, SUBDIR_CRASH_DUMPS]

        with pytest.raises(OSError):
            sb.ensure()

    def test_ensure_error_message_contains_path(self, tmp_path):
        """Error message should include the relevant path for diagnostics."""
        blocking_file = tmp_path / "blocking-file"
        blocking_file.write_text("block")

        sb = Sandbox.__new__(Sandbox)
        sb._app_name = "error-test"
        sb._base_dir = str(blocking_file / "subdir")
        sb._subdirs = [SUBDIR_DATA]

        with pytest.raises(OSError) as exc_info:
            sb.ensure()

        error_msg = str(exc_info.value)
        # The error message wraps the path with repr() (via !r), which escapes
        # backslashes on Windows. Check for a path-fragment that is
        # separator-free to avoid \\ vs \ mismatch.
        assert "blocking-file" in error_msg
        assert "sandbox" in error_msg
