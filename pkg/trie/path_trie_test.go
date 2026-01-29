/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package trie

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
)

// testValue is a simple value type for pure trie tests without channel dependency.
type testValue struct {
	id   int
	name string
}

// createTestValue creates a test value with the given id.
func createTestValue(id int) *testValue {
	return &testValue{id: id, name: fmt.Sprintf("value-%d", id)}
}

func TestPathTrie_BasicOperations(t *testing.T) {
	t.Run("insert and get returns same value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}
		val := createTestValue(1)

		trie.Insert(keyPath, val)

		retrieved, ok := trie.Get(keyPath)
		assert.True(t, ok)
		assert.Same(t, val, retrieved) // Same pointer
	})

	t.Run("len returns correct count", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		assert.Equal(t, 0, trie.Len())

		trie.Insert([]string{"project-1", "room-1"}, createTestValue(1))
		assert.Equal(t, 1, trie.Len())

		trie.Insert([]string{"project-1", "room-2"}, createTestValue(2))
		assert.Equal(t, 2, trie.Len())

		trie.Insert([]string{"project-1", "room-3"}, createTestValue(3))
		assert.Equal(t, 3, trie.Len())
	})

	t.Run("delete removes value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}

		trie.Insert(keyPath, createTestValue(1))
		assert.Equal(t, 1, trie.Len())

		trie.Delete(keyPath)
		_, ok := trie.Get(keyPath)
		assert.False(t, ok)
		assert.Equal(t, 0, trie.Len())
	})

	t.Run("forEach iterates all values", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		for i := range 5 {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			trie.Insert(keyPath, createTestValue(i))
		}

		count := 0
		trie.ForEach(func(v *testValue) bool {
			count++
			assert.NotNil(t, v)
			return true
		})
		assert.Equal(t, 5, count)
	})

	t.Run("get non-existent returns false", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		_, ok := trie.Get([]string{"project-1", "non-existent"})
		assert.False(t, ok)
	})

	t.Run("insert updates existing value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}

		val1 := createTestValue(1)
		val2 := createTestValue(2)

		trie.Insert(keyPath, val1)
		retrieved1, ok1 := trie.Get(keyPath)
		assert.True(t, ok1)
		assert.Same(t, val1, retrieved1)

		trie.Insert(keyPath, val2)
		retrieved2, ok2 := trie.Get(keyPath)
		assert.True(t, ok2)
		assert.Same(t, val2, retrieved2)
		assert.Equal(t, 1, trie.Len())
	})
}

func TestPathTrie_GetOrInsert(t *testing.T) {
	t.Run("inserts when not exists", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}

		createCount := 0
		expectedVal := createTestValue(1)
		val := trie.GetOrInsert(keyPath, func() *testValue {
			createCount++
			return expectedVal
		})

		assert.Same(t, expectedVal, val)
		assert.Equal(t, 1, createCount)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("returns existing without calling create", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}

		// First insert
		firstVal := createTestValue(1)
		val1 := trie.GetOrInsert(keyPath, func() *testValue {
			return firstVal
		})

		// Second call should not call create
		createCount := 0
		val2 := trie.GetOrInsert(keyPath, func() *testValue {
			createCount++
			return createTestValue(2)
		})

		assert.Equal(t, 0, createCount)
		assert.Same(t, val1, val2)
		assert.Same(t, firstVal, val2)
	})

	t.Run("returns zero value for empty keyPath", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		createCount := 0
		val := trie.GetOrInsert([]string{}, func() *testValue {
			createCount++
			return createTestValue(1)
		})

		assert.Nil(t, val)
		assert.Equal(t, 0, createCount)
	})

	t.Run("handles nil create function result", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		keyPath := []string{"project-1", "room-1"}

		val := trie.GetOrInsert(keyPath, func() *testValue {
			return nil
		})

		assert.Nil(t, val)
		// Note: nil is still inserted as a value
	})
}

func TestPathTrie_Hierarchy(t *testing.T) {
	t.Run("stores values at different depths", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val1 := createTestValue(1)
		val2 := createTestValue(2)
		val3 := createTestValue(3)

		trie.Insert([]string{"project-1", "room-1"}, val1)
		trie.Insert([]string{"project-1", "room-1", "section-1"}, val2)
		trie.Insert([]string{"project-1", "room-1", "section-1", "desk-1"}, val3)

		retrieved1, ok1 := trie.Get([]string{"project-1", "room-1"})
		assert.True(t, ok1)
		assert.Same(t, val1, retrieved1)

		retrieved2, ok2 := trie.Get([]string{"project-1", "room-1", "section-1"})
		assert.True(t, ok2)
		assert.Same(t, val2, retrieved2)

		retrieved3, ok3 := trie.Get([]string{"project-1", "room-1", "section-1", "desk-1"})
		assert.True(t, ok3)
		assert.Same(t, val3, retrieved3)

		assert.Equal(t, 3, trie.Len())
	})

	t.Run("delete parent preserves children", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val1 := createTestValue(1)
		val2 := createTestValue(2)

		trie.Insert([]string{"project-1", "room-1"}, val1)
		trie.Insert([]string{"project-1", "room-1", "section-1"}, val2)

		// Delete parent
		trie.Delete([]string{"project-1", "room-1"})

		// Parent should be deleted, child should still exist
		_, ok1 := trie.Get([]string{"project-1", "room-1"})
		assert.False(t, ok1)

		retrieved2, ok2 := trie.Get([]string{"project-1", "room-1", "section-1"})
		assert.True(t, ok2)
		assert.Same(t, val2, retrieved2)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("delete child preserves parent", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val1 := createTestValue(1)
		val2 := createTestValue(2)

		trie.Insert([]string{"project-1", "room-1"}, val1)
		trie.Insert([]string{"project-1", "room-1", "section-1"}, val2)

		// Delete child
		trie.Delete([]string{"project-1", "room-1", "section-1"})

		// Child should be deleted, parent should still exist
		retrieved1, ok1 := trie.Get([]string{"project-1", "room-1"})
		assert.True(t, ok1)
		assert.Same(t, val1, retrieved1)

		_, ok2 := trie.Get([]string{"project-1", "room-1", "section-1"})
		assert.False(t, ok2)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("deep path with 10 levels", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		keyPath := []string{"project-1"}
		for i := range 10 {
			keyPath = append(keyPath, fmt.Sprintf("level-%d", i))
		}

		val := createTestValue(1)
		trie.Insert(keyPath, val)

		retrieved, ok := trie.Get(keyPath)
		assert.True(t, ok)
		assert.Same(t, val, retrieved)
		assert.Equal(t, 1, trie.Len())
	})
}

func TestPathTrie_Traversal(t *testing.T) {
	t.Run("forEachDescendant traverses subtree", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// Insert hierarchical values
		trie.Insert([]string{"project-1", "room-1"}, createTestValue(1))
		trie.Insert([]string{"project-1", "room-1", "section-1"}, createTestValue(2))
		trie.Insert([]string{"project-1", "room-1", "section-1", "desk-1"}, createTestValue(3))
		trie.Insert([]string{"project-1", "room-1", "section-2"}, createTestValue(4))
		trie.Insert([]string{"project-1", "room-2"}, createTestValue(5)) // Different branch

		count := 0
		trie.ForEachDescendant([]string{"project-1", "room-1"}, func(v *testValue) bool {
			count++
			return true
		})
		assert.Equal(t, 4, count) // room-1 + section-1 + desk-1 + section-2 (not room-2)
	})

	t.Run("forEachDescendant traverses exact subtree", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		trie.Insert([]string{"project-1", "room-1", "section-1"}, createTestValue(1))
		trie.Insert([]string{"project-1", "room-1", "section-1", "desk-1"}, createTestValue(2))
		trie.Insert([]string{"project-1", "room-1", "section-2"}, createTestValue(3))

		count := 0
		trie.ForEachDescendant([]string{"project-1", "room-1", "section-1"}, func(v *testValue) bool {
			count++
			return true
		})
		assert.Equal(t, 2, count) // section-1 + desk-1
	})

	t.Run("forEachDescendant returns zero for non-existent path", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		trie.Insert([]string{"project-1", "room-1"}, createTestValue(1))

		count := 0
		trie.ForEachDescendant([]string{"project-1", "non-existent"}, func(v *testValue) bool {
			count++
			return true
		})
		assert.Equal(t, 0, count)
	})

	t.Run("forEach with early termination", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		for i := range 10 {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			trie.Insert(keyPath, createTestValue(i))
		}

		count := 0
		trie.ForEach(func(v *testValue) bool {
			count++
			return count < 5
		})
		assert.Equal(t, 5, count)
	})

	t.Run("forEachDescendant with early termination", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		for i := range 10 {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			trie.Insert(keyPath, createTestValue(i))
		}

		count := 0
		trie.ForEachDescendant([]string{"project-1"}, func(v *testValue) bool {
			count++
			return count < 3
		})
		assert.Equal(t, 3, count)
	})

}

func TestPathTrie_ProjectIsolation(t *testing.T) {
	t.Run("same key different projects coexist", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val1 := createTestValue(1)
		val2 := createTestValue(2)

		trie.Insert([]string{"project-1", "room-1"}, val1)
		trie.Insert([]string{"project-2", "room-1"}, val2)

		retrieved1, ok1 := trie.Get([]string{"project-1", "room-1"})
		retrieved2, ok2 := trie.Get([]string{"project-2", "room-1"})

		assert.True(t, ok1)
		assert.True(t, ok2)
		assert.Same(t, val1, retrieved1)
		assert.Same(t, val2, retrieved2)
		assert.NotSame(t, retrieved1, retrieved2)
		assert.Equal(t, 2, trie.Len())
	})

	t.Run("delete in one project does not affect another", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val1 := createTestValue(1)
		val2 := createTestValue(2)

		trie.Insert([]string{"project-1", "room-1"}, val1)
		trie.Insert([]string{"project-2", "room-1"}, val2)

		// Delete from project-1
		trie.Delete([]string{"project-1", "room-1"})

		// project-1's value should be gone
		_, ok1 := trie.Get([]string{"project-1", "room-1"})
		assert.False(t, ok1)

		// project-2's value should still exist
		retrieved2, ok2 := trie.Get([]string{"project-2", "room-1"})
		assert.True(t, ok2)
		assert.Same(t, val2, retrieved2)
		assert.Equal(t, 1, trie.Len())
	})
}

func TestPathTrie_EdgeCases(t *testing.T) {
	t.Run("empty keyPath operations", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// Insert with empty keyPath should not insert
		trie.Insert([]string{}, createTestValue(1))
		assert.Equal(t, 0, trie.Len())

		// Get with empty keyPath should return false
		_, ok := trie.Get([]string{})
		assert.False(t, ok)

		// Delete with empty keyPath should not panic
		trie.Delete([]string{})
		assert.Equal(t, 0, trie.Len())
	})

	t.Run("nil keyPath operations", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// Insert with nil keyPath should not insert
		trie.Insert(nil, createTestValue(1))
		assert.Equal(t, 0, trie.Len())

		// Get with nil keyPath should return false
		_, ok := trie.Get(nil)
		assert.False(t, ok)

		// Delete with nil keyPath should not panic
		trie.Delete(nil)
		assert.Equal(t, 0, trie.Len())
	})

	t.Run("delete non-existent does not panic", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// Should not panic
		trie.Delete([]string{"project-1", "non-existent"})
		assert.Equal(t, 0, trie.Len())
	})

	t.Run("forEach on empty trie", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		count := 0
		trie.ForEach(func(v *testValue) bool {
			count++
			return true
		})
		assert.Equal(t, 0, count)
	})

	t.Run("forEachDescendant on empty trie", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		count := 0
		trie.ForEachDescendant([]string{"project-1"}, func(v *testValue) bool {
			count++
			return true
		})
		assert.Equal(t, 0, count)
	})

	t.Run("single element keyPath", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		val := createTestValue(1)
		trie.Insert([]string{"project-1"}, val)

		retrieved, ok := trie.Get([]string{"project-1"})
		assert.True(t, ok)
		assert.Same(t, val, retrieved)
		assert.Equal(t, 1, trie.Len())

		trie.Delete([]string{"project-1"})
		_, ok = trie.Get([]string{"project-1"})
		assert.False(t, ok)
		assert.Equal(t, 0, trie.Len())
	})
}

func TestPathTrie_Concurrency(t *testing.T) {
	t.Run("concurrent insert same key", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		goroutines := 100
		var wg sync.WaitGroup

		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				keyPath := []string{"project-1", "room-1"}
				trie.Insert(keyPath, createTestValue(1))
			}()
		}

		wg.Wait()

		_, ok := trie.Get([]string{"project-1", "room-1"})
		assert.True(t, ok)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("concurrent getOrInsert same key - create called once", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		goroutines := 100
		var wg sync.WaitGroup
		var createCount atomic.Int32

		values := make([]*testValue, goroutines)

		for i := range goroutines {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", "room-1"}
				val := trie.GetOrInsert(keyPath, func() *testValue {
					createCount.Add(1)
					return createTestValue(1)
				})
				values[idx] = val
			}(i)
		}

		wg.Wait()

		// Create function should be called exactly once
		assert.Equal(t, int32(1), createCount.Load())

		// All goroutines should get the same value instance
		firstValue := values[0]
		for i := 1; i < goroutines; i++ {
			assert.Same(t, firstValue, values[i])
		}
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("concurrent read and write different keys", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		goroutines := 50
		var wg sync.WaitGroup

		// Insert initial values
		for i := range 10 {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			trie.Insert(keyPath, createTestValue(i))
		}

		// Concurrent reads
		for i := range goroutines {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", fmt.Sprintf("room-%d", idx%10)}
				val, ok := trie.Get(keyPath)
				assert.True(t, ok)
				assert.NotNil(t, val)
			}(i)
		}

		// Concurrent writes
		for i := range goroutines {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", fmt.Sprintf("room-new-%d", idx)}
				trie.Insert(keyPath, createTestValue(idx))
			}(i)
		}

		// Concurrent ForEach
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				count := 0
				trie.ForEach(func(v *testValue) bool {
					count++
					return true
				})
				// At minimum, we should see the initial 10 values
				// At maximum, we could see all 10 + goroutines values
				assert.GreaterOrEqual(t, count, 10, "should see at least initial values")
				assert.LessOrEqual(t, count, 10+goroutines, "should not exceed total values")
			}()
		}

		wg.Wait()
		assert.Equal(t, 10+goroutines, trie.Len())
	})

	t.Run("concurrent delete", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		valueCount := 100
		var wg sync.WaitGroup

		// Insert values
		for i := range valueCount {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			trie.Insert(keyPath, createTestValue(i))
		}
		assert.Equal(t, valueCount, trie.Len())

		// Concurrent deletes
		for i := range valueCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", fmt.Sprintf("room-%d", idx)}
				trie.Delete(keyPath)
			}(i)
		}

		wg.Wait()

		assert.Equal(t, 0, trie.Len())
		for i := range valueCount {
			keyPath := []string{"project-1", fmt.Sprintf("room-%d", i)}
			_, ok := trie.Get(keyPath)
			assert.False(t, ok)
		}
	})

	t.Run("concurrent insert and delete same key", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		iterations := 100
		var wg sync.WaitGroup

		keyPath := []string{"project-1", "room-1"}

		for range iterations {
			wg.Add(2)
			go func() {
				defer wg.Done()
				trie.Insert(keyPath, createTestValue(1))
			}()
			go func() {
				defer wg.Done()
				trie.Delete(keyPath)
			}()
		}

		wg.Wait()
		// The final state is non-deterministic, but trie should be consistent
		assert.LessOrEqual(t, trie.Len(), 1)
	})

	t.Run("stress test with mixed operations", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		operations := 1000
		var wg sync.WaitGroup

		for i := range operations {
			wg.Add(3)

			// Insert
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", fmt.Sprintf("room-%d", idx%100)}
				trie.Insert(keyPath, createTestValue(idx%100))
			}(i)

			// Read
			go func(idx int) {
				defer wg.Done()
				keyPath := []string{"project-1", fmt.Sprintf("room-%d", idx%100)}
				_, _ = trie.Get(keyPath)
			}(i)

			// ForEach
			go func() {
				defer wg.Done()
				trie.ForEach(func(v *testValue) bool {
					return true
				})
			}()
		}

		wg.Wait()
		// All inserts use room-{idx%100}, so exactly 100 unique keys
		assert.Equal(t, 100, trie.Len())
	})
}

func TestPathTrie_RootValue(t *testing.T) {
	t.Run("GetRoot on empty trie returns false", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		_, ok := trie.GetRoot()
		assert.False(t, ok)
	})

	t.Run("GetOrInsertRoot creates and returns value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		createCount := 0

		val := trie.GetOrInsertRoot(func() *testValue {
			createCount++
			return createTestValue(1)
		})

		assert.NotNil(t, val)
		assert.Equal(t, 1, val.id)
		assert.Equal(t, 1, createCount)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("GetOrInsertRoot returns existing without calling create", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		createCount := 0

		// First call creates value
		val1 := trie.GetOrInsertRoot(func() *testValue {
			createCount++
			return createTestValue(1)
		})

		// Second call returns existing
		val2 := trie.GetOrInsertRoot(func() *testValue {
			createCount++
			return createTestValue(2)
		})

		assert.Same(t, val1, val2)
		assert.Equal(t, 1, createCount)
	})

	t.Run("GetRoot returns value after GetOrInsertRoot", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		inserted := trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(42)
		})

		retrieved, ok := trie.GetRoot()
		assert.True(t, ok)
		assert.Same(t, inserted, retrieved)
	})

	t.Run("DeleteRoot removes value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(1)
		})
		assert.Equal(t, 1, trie.Len())

		deleted := trie.DeleteRoot()
		assert.True(t, deleted)
		assert.Equal(t, 0, trie.Len())

		_, ok := trie.GetRoot()
		assert.False(t, ok)
	})

	t.Run("DeleteRoot on empty trie returns false", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		deleted := trie.DeleteRoot()
		assert.False(t, deleted)
	})

	t.Run("root value coexists with child values", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		rootVal := trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(0)
		})
		trie.Insert([]string{"child"}, createTestValue(1))
		trie.Insert([]string{"child", "grandchild"}, createTestValue(2))

		assert.Equal(t, 3, trie.Len())

		retrieved, ok := trie.GetRoot()
		assert.True(t, ok)
		assert.Same(t, rootVal, retrieved)

		child, ok := trie.Get([]string{"child"})
		assert.True(t, ok)
		assert.Equal(t, 1, child.id)
	})

	t.Run("delete root preserves children", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(0)
		})
		trie.Insert([]string{"child"}, createTestValue(1))
		assert.Equal(t, 2, trie.Len())

		trie.DeleteRoot()
		assert.Equal(t, 1, trie.Len())

		_, ok := trie.GetRoot()
		assert.False(t, ok)

		child, ok := trie.Get([]string{"child"})
		assert.True(t, ok)
		assert.Equal(t, 1, child.id)
	})

	t.Run("concurrent GetOrInsertRoot calls create once", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		var createCount int32 = 0
		var wg sync.WaitGroup

		for range 100 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				trie.GetOrInsertRoot(func() *testValue {
					atomic.AddInt32(&createCount, 1)
					return createTestValue(1)
				})
			}()
		}

		wg.Wait()
		assert.Equal(t, int32(1), createCount)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("InsertRoot creates value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		trie.InsertRoot(createTestValue(42))

		retrieved, ok := trie.GetRoot()
		assert.True(t, ok)
		assert.Equal(t, 42, retrieved.id)
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("InsertRoot overwrites existing value", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// First insert
		trie.InsertRoot(createTestValue(100))
		val, ok := trie.GetRoot()
		assert.True(t, ok)
		assert.Equal(t, 100, val.id)

		// Overwrite with new value
		trie.InsertRoot(createTestValue(200))
		val, ok = trie.GetRoot()
		assert.True(t, ok)
		assert.Equal(t, 200, val.id)

		// Should still be only 1 value
		assert.Equal(t, 1, trie.Len())
	})

	t.Run("InsertRoot vs GetOrInsertRoot behavior difference", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// GetOrInsertRoot creates initial value
		trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(100)
		})
		val, _ := trie.GetRoot()
		assert.Equal(t, 100, val.id)

		// GetOrInsertRoot does NOT overwrite (returns existing)
		trie.GetOrInsertRoot(func() *testValue {
			return createTestValue(200)
		})
		val, _ = trie.GetRoot()
		assert.Equal(t, 100, val.id) // Still 100

		// InsertRoot DOES overwrite
		trie.InsertRoot(createTestValue(300))
		val, _ = trie.GetRoot()
		assert.Equal(t, 300, val.id) // Now 300
	})

	t.Run("InsertRoot preserves children", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()

		// Insert children first
		trie.Insert([]string{"child1"}, createTestValue(1))
		trie.Insert([]string{"child2"}, createTestValue(2))
		assert.Equal(t, 2, trie.Len())

		// Insert root value
		trie.InsertRoot(createTestValue(0))
		assert.Equal(t, 3, trie.Len())

		// Children should still exist
		child1, ok := trie.Get([]string{"child1"})
		assert.True(t, ok)
		assert.Equal(t, 1, child1.id)

		child2, ok := trie.Get([]string{"child2"})
		assert.True(t, ok)
		assert.Equal(t, 2, child2.id)
	})

	t.Run("concurrent InsertRoot", func(t *testing.T) {
		trie := NewPathTrie[*testValue]()
		var wg sync.WaitGroup

		for i := range 100 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				trie.InsertRoot(createTestValue(idx))
			}(i)
		}

		wg.Wait()

		// Should have exactly 1 value (last write wins)
		assert.Equal(t, 1, trie.Len())

		// Value should be one of the inserted values
		_, ok := trie.GetRoot()
		assert.True(t, ok)
	})
}
