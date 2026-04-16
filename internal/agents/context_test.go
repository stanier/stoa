package agents

import (
	"sync"
	"testing"
)

func TestSharedContext_SetGet(t *testing.T) {
	ctx := NewSharedContext()

	// Test basic set/get
	ctx.Set("key1", "value1")
	ctx.Set("key2", 42)
	ctx.Set("key3", true)

	value, ok := ctx.Get("key1")
	if !ok {
		t.Error("expected to find key1")
	}
	if value != "value1" {
		t.Errorf("expected 'value1', got '%v'", value)
	}

	value, ok = ctx.Get("key2")
	if !ok {
		t.Error("expected to find key2")
	}
	if value != 42 {
		t.Errorf("expected 42, got %v", value)
	}

	value, ok = ctx.Get("key3")
	if !ok {
		t.Error("expected to find key3")
	}
	if value != true {
		t.Errorf("expected true, got %v", value)
	}

	// Test non-existent key
	_, ok = ctx.Get("nonexistent")
	if ok {
		t.Error("expected not to find nonexistent key")
	}
}

func TestSharedContext_GetOrDefault(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")

	// Existing key
	value := ctx.GetOrDefault("key1", "default")
	if value != "value1" {
		t.Errorf("expected 'value1', got '%v'", value)
	}

	// Non-existent key
	value = ctx.GetOrDefault("nonexistent", "default")
	if value != "default" {
		t.Errorf("expected 'default', got '%v'", value)
	}
}

func TestSharedContext_Has(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")

	if !ctx.Has("key1") {
		t.Error("expected Has to return true for key1")
	}

	if ctx.Has("nonexistent") {
		t.Error("expected Has to return false for nonexistent key")
	}
}

func TestSharedContext_Delete(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")
	ctx.Set("key2", "value2")

	// Delete key1
	ctx.Delete("key1")

	if ctx.Has("key1") {
		t.Error("expected key1 to be deleted")
	}

	if !ctx.Has("key2") {
		t.Error("expected key2 to still exist")
	}

	// Delete non-existent key (should not error)
	ctx.Delete("nonexistent")
}

func TestSharedContext_Keys(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")
	ctx.Set("key2", "value2")
	ctx.Set("key3", "value3")

	keys := ctx.Keys()

	if len(keys) != 3 {
		t.Errorf("expected 3 keys, got %d", len(keys))
	}

	// Check all keys are present
	keyMap := make(map[string]bool)
	for _, key := range keys {
		keyMap[key] = true
	}

	if !keyMap["key1"] || !keyMap["key2"] || !keyMap["key3"] {
		t.Error("expected all keys to be in the keys list")
	}
}

func TestSharedContext_Clear(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")
	ctx.Set("key2", "value2")
	ctx.Set("key3", "value3")

	if ctx.Size() != 3 {
		t.Errorf("expected size 3, got %d", ctx.Size())
	}

	ctx.Clear()

	if ctx.Size() != 0 {
		t.Errorf("expected size 0 after clear, got %d", ctx.Size())
	}

	if ctx.Has("key1") || ctx.Has("key2") || ctx.Has("key3") {
		t.Error("expected all keys to be cleared")
	}
}

func TestSharedContext_Size(t *testing.T) {
	ctx := NewSharedContext()

	if ctx.Size() != 0 {
		t.Errorf("expected initial size 0, got %d", ctx.Size())
	}

	ctx.Set("key1", "value1")
	if ctx.Size() != 1 {
		t.Errorf("expected size 1, got %d", ctx.Size())
	}

	ctx.Set("key2", "value2")
	if ctx.Size() != 2 {
		t.Errorf("expected size 2, got %d", ctx.Size())
	}

	ctx.Delete("key1")
	if ctx.Size() != 1 {
		t.Errorf("expected size 1 after delete, got %d", ctx.Size())
	}
}

func TestSharedContext_Copy(t *testing.T) {
	ctx := NewSharedContext()

	ctx.Set("key1", "value1")
	ctx.Set("key2", 42)

	copy := ctx.Copy()

	if len(copy) != 2 {
		t.Errorf("expected copy to have 2 items, got %d", len(copy))
	}

	if copy["key1"] != "value1" {
		t.Errorf("expected copy['key1'] to be 'value1', got '%v'", copy["key1"])
	}

	if copy["key2"] != 42 {
		t.Errorf("expected copy['key2'] to be 42, got %v", copy["key2"])
	}

	// Modify copy should not affect original
	copy["key3"] = "value3"

	if ctx.Has("key3") {
		t.Error("expected original context not to have key3")
	}
}

func TestSharedContext_Concurrent(t *testing.T) {
	ctx := NewSharedContext()
	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			ctx.Set("key", n)
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx.Get("key")
		}()
	}

	// Concurrent deletes
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx.Delete("key")
		}()
	}

	wg.Wait()

	// Should not panic or race
	// The final value is non-deterministic due to concurrent writes
	_ = ctx.Size()
}

func TestSharedContext_ComplexTypes(t *testing.T) {
	ctx := NewSharedContext()

	// Test with struct
	type TestStruct struct {
		Name  string
		Value int
	}

	testStruct := TestStruct{Name: "test", Value: 42}
	ctx.Set("struct", testStruct)

	value, ok := ctx.Get("struct")
	if !ok {
		t.Fatal("expected to find struct")
	}

	retrieved, ok := value.(TestStruct)
	if !ok {
		t.Fatal("expected value to be TestStruct")
	}

	if retrieved.Name != "test" || retrieved.Value != 42 {
		t.Errorf("expected {Name: test, Value: 42}, got %+v", retrieved)
	}

	// Test with slice
	testSlice := []string{"a", "b", "c"}
	ctx.Set("slice", testSlice)

	value, ok = ctx.Get("slice")
	if !ok {
		t.Fatal("expected to find slice")
	}

	retrievedSlice, ok := value.([]string)
	if !ok {
		t.Fatal("expected value to be []string")
	}

	if len(retrievedSlice) != 3 || retrievedSlice[0] != "a" {
		t.Errorf("unexpected slice contents: %v", retrievedSlice)
	}

	// Test with map
	testMap := map[string]int{"one": 1, "two": 2}
	ctx.Set("map", testMap)

	value, ok = ctx.Get("map")
	if !ok {
		t.Fatal("expected to find map")
	}

	retrievedMap, ok := value.(map[string]int)
	if !ok {
		t.Fatal("expected value to be map[string]int")
	}

	if retrievedMap["one"] != 1 || retrievedMap["two"] != 2 {
		t.Errorf("unexpected map contents: %v", retrievedMap)
	}
}
