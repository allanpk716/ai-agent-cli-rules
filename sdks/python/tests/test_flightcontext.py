"""Tests for agentsdk.flightcontext — Thread-safe key-value store.

Covers: empty state, get/set/delete, overwrite, delete-missing, snapshot
        isolation, snapshot copies all keys, concurrent access, empty
        snapshot, and large values.
"""

import threading
from typing import Any, Dict

import pytest

from agentsdk.flightcontext import FlightContext


# ---------------------------------------------------------------------------
# Empty state
# ---------------------------------------------------------------------------

class TestFlightContextEmpty:
    def test_new_is_empty(self):
        fc = FlightContext()
        snap = fc.snapshot()
        assert len(snap) == 0

    def test_empty_snapshot_is_non_none(self):
        fc = FlightContext()
        snap = fc.snapshot()
        assert snap is not None
        assert isinstance(snap, dict)


# ---------------------------------------------------------------------------
# Get / Set
# ---------------------------------------------------------------------------

class TestFlightContextGetSet:
    def test_get_missing_returns_none(self):
        fc = FlightContext()
        assert fc.get("nope") is None

    def test_set_and_get(self):
        fc = FlightContext()
        fc.set("key1", "value1")
        fc.set("key2", 42)

        assert fc.get("key1") == "value1"
        assert fc.get("key2") == 42

    def test_set_overwrites(self):
        fc = FlightContext()
        fc.set("key", "first")
        fc.set("key", "second")
        assert fc.get("key") == "second"


# ---------------------------------------------------------------------------
# Delete
# ---------------------------------------------------------------------------

class TestFlightContextDelete:
    def test_delete_removes_key(self):
        fc = FlightContext()
        fc.set("key", "value")
        fc.delete("key")
        assert fc.get("key") is None

    def test_delete_missing_is_noop(self):
        fc = FlightContext()
        # Should not raise
        fc.delete("nonexistent")


# ---------------------------------------------------------------------------
# Snapshot
# ---------------------------------------------------------------------------

class TestFlightContextSnapshot:
    def test_snapshot_isolation_mutation_does_not_affect_original(self):
        fc = FlightContext()
        fc.set("a", 1)
        fc.set("b", 2)

        snap = fc.snapshot()

        # Mutating the snapshot should not affect the original.
        snap["a"] = "changed"
        snap["c"] = 3

        assert fc.get("a") == 1
        assert fc.get("c") is None

    def test_snapshot_isolation_original_does_not_affect_snapshot(self):
        fc = FlightContext()
        fc.set("a", 1)

        snap = fc.snapshot()

        # Mutating the original should not affect the snapshot.
        fc.set("d", 4)
        assert "d" not in snap

    def test_snapshot_copies_all_keys(self):
        fc = FlightContext()
        fc.set("x", 10)
        fc.set("y", "hello")
        fc.set("z", True)

        snap = fc.snapshot()
        assert len(snap) == 3
        assert snap["x"] == 10
        assert snap["y"] == "hello"
        assert snap["z"] is True


# ---------------------------------------------------------------------------
# Concurrent access
# ---------------------------------------------------------------------------

class TestFlightContextConcurrency:
    def test_concurrent_get_set_delete(self):
        """Concurrent operations should not cause panics or corruption."""
        fc = FlightContext()
        num_threads = 50
        ops_per_thread = 100
        barrier = threading.Barrier(num_threads * 3)

        def writer(thread_id: int) -> None:
            barrier.wait()
            for j in range(ops_per_thread):
                fc.set(f"key-{thread_id}-{j}", j)

        def reader(thread_id: int) -> None:
            barrier.wait()
            for j in range(ops_per_thread):
                fc.get(f"key-{thread_id}-{j}")

        def deleter(thread_id: int) -> None:
            barrier.wait()
            for j in range(ops_per_thread):
                fc.delete(f"key-{thread_id}-{j}")

        threads: list[threading.Thread] = []
        for i in range(num_threads):
            threads.append(threading.Thread(target=writer, args=(i,)))
            threads.append(threading.Thread(target=reader, args=(i,)))
            threads.append(threading.Thread(target=deleter, args=(i,)))

        for t in threads:
            t.start()
        for t in threads:
            t.join()

        # After all operations, snapshot should be a valid dict (no corruption).
        snap = fc.snapshot()
        assert isinstance(snap, dict)


# ---------------------------------------------------------------------------
# Large values
# ---------------------------------------------------------------------------

class TestFlightContextLargeValues:
    def test_large_string_value(self):
        fc = FlightContext()
        # 1 MB string
        large_str = "".join(chr(ord("A") + i % 26) for i in range(1024 * 1024))

        fc.set("big", large_str)
        assert fc.get("big") == large_str

        snap = fc.snapshot()
        assert snap["big"] == large_str


# ---------------------------------------------------------------------------
# Clear
# ---------------------------------------------------------------------------


class TestClear:
    def test_clear_empties_populated_context(self):
        fc = FlightContext()
        fc.set("a", 1)
        fc.set("b", "two")
        fc.clear()
        assert fc.snapshot() == {}

    def test_clear_on_empty_is_noop(self):
        fc = FlightContext()
        fc.clear()
        assert fc.snapshot() == {}

    def test_clear_then_set_works(self):
        fc = FlightContext()
        fc.set("x", 1)
        fc.clear()
        fc.set("y", 2)
        assert fc.snapshot() == {"y": 2}
