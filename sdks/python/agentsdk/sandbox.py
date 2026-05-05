"""Sandbox — application directory manager.

Manages the application's sandbox directory (``~/.app-name/``) and its
standard subdirectories (data, cache, locks, crash_dumps).

The base directory defaults to ``~/.<app-name>/`` and can be overridden by
setting the environment variable ``<APP_NAME>_HOME`` (uppercased app name,
hyphens replaced with underscores). For example, an app named ``my-tool``
would respect the ``MY_TOOL_HOME`` environment variable.
"""

from __future__ import annotations

import os
from pathlib import Path
from typing import Dict

# Standard subdirectory names.
SUBDIR_DATA = "data"
SUBDIR_CACHE = "cache"
SUBDIR_LOCKS = "locks"
SUBDIR_CRASH_DUMPS = "crash_dumps"

_STANDARD_SUBDIRS = [SUBDIR_DATA, SUBDIR_CACHE, SUBDIR_LOCKS, SUBDIR_CRASH_DUMPS]


class Sandbox:
    """Manages the application's sandbox directory and its standard subdirectories."""

    def __init__(self, app_name: str) -> None:
        self._app_name = app_name
        self._subdirs = list(_STANDARD_SUBDIRS)

        # Check for environment variable override.
        env_var = app_name.upper().replace("-", "_") + "_HOME"
        env_dir = os.environ.get(env_var)
        if env_dir:
            self._base_dir = env_dir
        else:
            self._base_dir = str(Path.home() / f".{app_name}")

    # -- Accessors -----------------------------------------------------------

    @property
    def base_dir(self) -> str:
        """Return the sandbox's base directory path."""
        return self._base_dir

    @property
    def data_dir(self) -> str:
        """Return the data subdirectory path."""
        return str(Path(self._base_dir) / SUBDIR_DATA)

    @property
    def cache_dir(self) -> str:
        """Return the cache subdirectory path."""
        return str(Path(self._base_dir) / SUBDIR_CACHE)

    @property
    def locks_dir(self) -> str:
        """Return the locks subdirectory path."""
        return str(Path(self._base_dir) / SUBDIR_LOCKS)

    @property
    def crash_dumps_dir(self) -> str:
        """Return the crash_dumps subdirectory path."""
        return str(Path(self._base_dir) / SUBDIR_CRASH_DUMPS)

    def dirs(self) -> Dict[str, str]:
        """Return a map of subdirectory names to their full paths.

        Useful for agent doctor checks and diagnostic tooling.
        """
        return {name: str(Path(self._base_dir) / name) for name in self._subdirs}

    # -- Mutation ------------------------------------------------------------

    def ensure(self) -> None:
        """Idempotently create the base directory and all subdirectories.

        Raises:
            OSError: If the directory cannot be created (e.g. a file exists
                where a directory is expected).
        """
        base = Path(self._base_dir)
        try:
            base.mkdir(parents=True, exist_ok=True)
        except OSError as exc:
            raise OSError(f"sandbox: create base dir {self._base_dir!r}: {exc}") from exc

        for name in self._subdirs:
            sub_path = base / name
            try:
                sub_path.mkdir(parents=True, exist_ok=True)
            except OSError as exc:
                raise OSError(f"sandbox: create subdir {str(sub_path)!r}: {exc}") from exc
