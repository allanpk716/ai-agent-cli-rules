package agentsdk

import "sync"

// FlightContext is a goroutine-safe key-value store for recording in-flight
// state that should be captured on crash (signal or panic).
//
// It uses sync.RWMutex to favor read-heavy workloads where agents frequently
// read context while only occasionally updating it. All methods are safe for
// concurrent use.
type FlightContext struct {
	mu   sync.RWMutex
	data map[string]interface{}
}

// NewFlightContext creates an empty FlightContext.
func NewFlightContext() *FlightContext {
	return &FlightContext{
		data: make(map[string]interface{}),
	}
}

// Get retrieves the value for the given key. Returns nil if the key does not exist.
func (fc *FlightContext) Get(key string) interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	return fc.data[key]
}

// Set stores a value for the given key, overwriting any existing value.
func (fc *FlightContext) Set(key string, value interface{}) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	fc.data[key] = value
}

// Delete removes the value for the given key. No-op if the key does not exist.
func (fc *FlightContext) Delete(key string) {
	fc.mu.Lock()
	defer fc.mu.Unlock()
	delete(fc.data, key)
}

// Snapshot returns a shallow copy of the current key-value map.
// The returned map is safe to mutate without affecting the FlightContext.
// Snapshot does NOT deep-copy interface{} values — callers storing mutable
// references (slices, maps, pointers) should be aware of this.
func (fc *FlightContext) Snapshot() map[string]interface{} {
	fc.mu.RLock()
	defer fc.mu.RUnlock()
	snapshot := make(map[string]interface{}, len(fc.data))
	for k, v := range fc.data {
		snapshot[k] = v
	}
	return snapshot
}
