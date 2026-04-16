package agents

import (
	"sync"
)

// SharedContext provides thread-safe shared data between agents
type SharedContext struct {
	data map[string]interface{}
	mu   sync.RWMutex
}

// NewSharedContext creates a new shared context
func NewSharedContext() *SharedContext {
	return &SharedContext{
		data: make(map[string]interface{}),
	}
}

// Set stores a value in the shared context
func (sc *SharedContext) Set(key string, value interface{}) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.data[key] = value
}

// Get retrieves a value from the shared context
func (sc *SharedContext) Get(key string) (interface{}, bool) {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	val, exists := sc.data[key]
	return val, exists
}

// GetOrDefault retrieves a value or returns the default
func (sc *SharedContext) GetOrDefault(key string, defaultValue interface{}) interface{} {
	val, exists := sc.Get(key)
	if !exists {
		return defaultValue
	}
	return val
}

// Delete removes a value from the shared context
func (sc *SharedContext) Delete(key string) {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	delete(sc.data, key)
}

// Keys returns all keys in the shared context
func (sc *SharedContext) Keys() []string {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	keys := make([]string, 0, len(sc.data))
	for k := range sc.data {
		keys = append(keys, k)
	}
	return keys
}

// Has checks if a key exists
func (sc *SharedContext) Has(key string) bool {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	_, exists := sc.data[key]
	return exists
}

// Clear removes all values from the shared context
func (sc *SharedContext) Clear() {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	sc.data = make(map[string]interface{})
}

// Size returns the number of items in the shared context
func (sc *SharedContext) Size() int {
	sc.mu.RLock()
	defer sc.mu.RUnlock()
	return len(sc.data)
}

// Copy returns a copy of all data
func (sc *SharedContext) Copy() map[string]interface{} {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	copy := make(map[string]interface{}, len(sc.data))
	for k, v := range sc.data {
		copy[k] = v
	}
	return copy
}
