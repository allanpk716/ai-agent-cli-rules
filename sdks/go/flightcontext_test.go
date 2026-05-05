package agentsdk

import (
	"fmt"
	"sync"
	"testing"
)

func TestFlightContextNewIsEmpty(t *testing.T) {
	fc := NewFlightContext()
	snap := fc.Snapshot()
	if len(snap) != 0 {
		t.Errorf("new FlightContext Snapshot() = %d items, want 0", len(snap))
	}
}

func TestFlightContextGetMissing(t *testing.T) {
	fc := NewFlightContext()
	if v := fc.Get("nope"); v != nil {
		t.Errorf("Get(missing) = %v, want nil", v)
	}
}

func TestFlightContextSetAndGet(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("key1", "value1")
	fc.Set("key2", 42)

	if v := fc.Get("key1"); v != "value1" {
		t.Errorf("Get(key1) = %v, want %q", v, "value1")
	}
	if v := fc.Get("key2"); v != 42 {
		t.Errorf("Get(key2) = %v, want 42", v)
	}
}

func TestFlightContextSetOverwrites(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("key", "first")
	fc.Set("key", "second")
	if v := fc.Get("key"); v != "second" {
		t.Errorf("Get(key) after overwrite = %v, want %q", v, "second")
	}
}

func TestFlightContextDelete(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("key", "value")
	fc.Delete("key")
	if v := fc.Get("key"); v != nil {
		t.Errorf("Get(key) after Delete = %v, want nil", v)
	}
}

func TestFlightContextDeleteMissing(t *testing.T) {
	fc := NewFlightContext()
	// Should not panic on missing key
	fc.Delete("nonexistent")
}

func TestFlightContextSnapshotIsolation(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("a", 1)
	fc.Set("b", 2)

	snap := fc.Snapshot()

	// Mutating the snapshot should not affect the original.
	snap["a"] = "changed"
	snap["c"] = 3

	if v := fc.Get("a"); v != 1 {
		t.Errorf("Get(a) after snapshot mutation = %v, want 1", v)
	}
	if v := fc.Get("c"); v != nil {
		t.Errorf("Get(c) after snapshot mutation = %v, want nil", v)
	}

	// Mutating the original should not affect the snapshot.
	fc.Set("d", 4)
	if _, ok := snap["d"]; ok {
		t.Error("snapshot should not contain key added after Snapshot() call")
	}
}

func TestFlightContextSnapshotCopiesAllKeys(t *testing.T) {
	fc := NewFlightContext()
	fc.Set("x", 10)
	fc.Set("y", "hello")
	fc.Set("z", true)

	snap := fc.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("Snapshot() has %d items, want 3", len(snap))
	}
	if snap["x"] != 10 {
		t.Errorf("Snapshot()[x] = %v, want 10", snap["x"])
	}
	if snap["y"] != "hello" {
		t.Errorf("Snapshot()[y] = %v, want %q", snap["y"], "hello")
	}
	if snap["z"] != true {
		t.Errorf("Snapshot()[z] = %v, want true", snap["z"])
	}
}

func TestFlightContextConcurrentGetSetDelete(t *testing.T) {
	fc := NewFlightContext()
	const goroutines = 50
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	// Concurrent writers
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				fc.Set(fmt.Sprintf("key-%d-%d", id, j), j)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				_ = fc.Get(fmt.Sprintf("key-%d-%d", id, j))
			}
		}(i)
	}

	// Concurrent deleters
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				fc.Delete(fmt.Sprintf("key-%d-%d", id, j))
			}
		}(i)
	}

	wg.Wait()

	// After all operations, the context should still be valid (no panics, no corruption).
	snap := fc.Snapshot()
	// We can't predict exact contents due to race between set and delete,
	// but the map should be non-nil and not corrupted.
	if snap == nil {
		t.Error("Snapshot() returned nil map after concurrent operations")
	}
}

func TestFlightContextEmptySnapshot(t *testing.T) {
	fc := NewFlightContext()
	snap := fc.Snapshot()
	if snap == nil {
		t.Error("Snapshot() returned nil, want empty non-nil map")
	}
	if len(snap) != 0 {
		t.Errorf("empty Snapshot() has %d items, want 0", len(snap))
	}
}

func TestFlightContextLargeValues(t *testing.T) {
	fc := NewFlightContext()

	// Store a large string value.
	largeVal := make([]byte, 1024*1024) // 1MB of zeros
	for i := range largeVal {
		largeVal[i] = 'A' + byte(i%26)
	}
	largeStr := string(largeVal)

	fc.Set("big", largeStr)
	if v := fc.Get("big"); v != largeStr {
		t.Error("Get(big) did not return the same large string")
	}

	snap := fc.Snapshot()
	if snap["big"] != largeStr {
		t.Error("Snapshot()[big] did not contain the same large string")
	}
}
