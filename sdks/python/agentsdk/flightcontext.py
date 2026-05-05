"""FlightContext — thread-safe key-value store for in-flight state.

Records in-flight state that should be captured on crash (signal or panic).
Uses ``threading.RLock`` for concurrent access safety, favoring read-heavy
workloads where agents frequently read context while only occasionally
updating it.

All methods are safe for concurrent use.
"""

from __future__ import annotations

import threading
from typing import Any, Dict, Optional


class FlightContext:
    """Thread-safe key-value store for recording in-flight state."""

    def __init__(self) -> None:
        self._mu: threading.RLock = threading.RLock()
        self._data: Dict[str, Any] = {}

    # -- Read operations -----------------------------------------------------

    def get(self, key: str) -> Optional[Any]:
        """Retrieve the value for *key*. Returns ``None`` if the key does not exist."""
        with self._mu:
            return self._data.get(key)

    def snapshot(self) -> Dict[str, Any]:
        """Return a shallow copy of the current key-value map.

        The returned dict is safe to mutate without affecting the
        FlightContext.  Snapshot does **not** deep-copy values — callers
        storing mutable references (lists, dicts) should be aware of this.
        """
        with self._mu:
            return dict(self._data)

    # -- Write operations ----------------------------------------------------

    def set(self, key: str, value: Any) -> None:
        """Store *value* for *key*, overwriting any existing value."""
        with self._mu:
            self._data[key] = value

    def delete(self, key: str) -> None:
        """Remove the value for *key*. No-op if the key does not exist."""
        with self._mu:
            self._data.pop(key, None)
