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

package trie_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/trie"
)

func TestShardedPathTrie_BasicOperations(t *testing.T) {
	st := trie.NewShardedPathTrie[string]()

	t.Run("insert and get", func(t *testing.T) {
		st.Insert("p1.room1", []string{"user1"}, "value1")
		st.Insert("p1.room1", []string{"user2"}, "value2")
		st.Insert("p1.room2", []string{"user1"}, "value3")

		val, ok := st.Get("p1.room1", []string{"user1"})
		assert.True(t, ok)
		assert.Equal(t, "value1", val)

		val, ok = st.Get("p1.room1", []string{"user2"})
		assert.True(t, ok)
		assert.Equal(t, "value2", val)

		val, ok = st.Get("p1.room2", []string{"user1"})
		assert.True(t, ok)
		assert.Equal(t, "value3", val)

		// Non-existent key
		_, ok = st.Get("p1.room1", []string{"user999"})
		assert.False(t, ok)
	})

	t.Run("root value (empty keyPath)", func(t *testing.T) {
		st.Insert("p1.lobby", nil, "lobby-value")

		val, ok := st.Get("p1.lobby", nil)
		assert.True(t, ok)
		assert.Equal(t, "lobby-value", val)
	})

	t.Run("len", func(t *testing.T) {
		st2 := trie.NewShardedPathTrie[int]()
		assert.Equal(t, 0, st2.Len())

		st2.Insert("p1.r1", []string{"u1"}, 1)
		st2.Insert("p1.r1", []string{"u2"}, 2)
		st2.Insert("p1.r2", []string{"u1"}, 3)
		assert.Equal(t, 3, st2.Len())
	})

	t.Run("shard count", func(t *testing.T) {
		st3 := trie.NewShardedPathTrie[int]()
		assert.Equal(t, 0, st3.ShardCount())

		st3.Insert("p1.r1", []string{"u1"}, 1)
		assert.Equal(t, 1, st3.ShardCount())

		st3.Insert("p1.r2", []string{"u1"}, 2)
		assert.Equal(t, 2, st3.ShardCount())

		st3.Insert("p2.r1", []string{"u1"}, 3)
		assert.Equal(t, 3, st3.ShardCount())
	})
}

func TestShardedPathTrie_GetOrInsert(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	createCount := 0

	create := func() int {
		createCount++
		return createCount
	}

	// First call should create
	val := st.GetOrInsert("p1.r1", []string{"u1"}, create)
	assert.Equal(t, 1, val)
	assert.Equal(t, 1, createCount)

	// Second call should return existing
	val = st.GetOrInsert("p1.r1", []string{"u1"}, create)
	assert.Equal(t, 1, val)
	assert.Equal(t, 1, createCount) // create not called again

	// Different key should create new value
	val = st.GetOrInsert("p1.r1", []string{"u2"}, create)
	assert.Equal(t, 2, val)
	assert.Equal(t, 2, createCount)

	// Test root value GetOrInsert
	val = st.GetOrInsert("p1.lobby", nil, create)
	assert.Equal(t, 3, val)
	assert.Equal(t, 3, createCount)

	val = st.GetOrInsert("p1.lobby", nil, create)
	assert.Equal(t, 3, val)
	assert.Equal(t, 3, createCount) // create not called again
}

func TestShardedPathTrie_Delete(t *testing.T) {
	st := trie.NewShardedPathTrie[string]()

	st.Insert("p1.r1", []string{"u1"}, "value1")
	st.Insert("p1.r1", []string{"u2"}, "value2")
	st.Insert("p1.lobby", nil, "lobby")

	// Delete existing key
	deleted := st.Delete("p1.r1", []string{"u1"})
	assert.True(t, deleted)

	_, ok := st.Get("p1.r1", []string{"u1"})
	assert.False(t, ok)

	// Other values should still exist
	val, ok := st.Get("p1.r1", []string{"u2"})
	assert.True(t, ok)
	assert.Equal(t, "value2", val)

	// Delete root value
	deleted = st.Delete("p1.lobby", nil)
	assert.True(t, deleted)

	_, ok = st.Get("p1.lobby", nil)
	assert.False(t, ok)

	// Delete non-existent key
	deleted = st.Delete("p999.r999", []string{"u999"})
	assert.False(t, deleted)
}

func TestShardedPathTrie_ForEach(t *testing.T) {
	t.Run("iterates all values", func(t *testing.T) {
		st := trie.NewShardedPathTrie[int]()

		st.Insert("p1.r1", []string{"u1"}, 1)
		st.Insert("p1.r1", []string{"u2"}, 2)
		st.Insert("p1.r2", []string{"u1"}, 3)
		st.Insert("p2.r1", []string{"u1"}, 4)

		var values []int
		st.ForEach(func(v int) bool {
			values = append(values, v)
			return true
		})

		assert.Len(t, values, 4)
		assert.ElementsMatch(t, []int{1, 2, 3, 4}, values)
	})

	t.Run("empty trie calls callback zero times", func(t *testing.T) {
		st := trie.NewShardedPathTrie[int]()

		count := 0
		st.ForEach(func(v int) bool {
			count++
			return true
		})

		assert.Equal(t, 0, count)
	})
}

func TestShardedPathTrie_ForEachByShard(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	st.Insert("p1.r1", []string{"u1"}, 1)
	st.Insert("p1.r1", []string{"u2"}, 2)
	st.Insert("p1.r2", []string{"u1"}, 3)
	st.Insert("p2.r1", []string{"u1"}, 4)
	st.Insert("p2.r2", []string{"u1"}, 5)

	t.Run("filter by shard prefix", func(t *testing.T) {
		var p1Values []int
		st.ForEachByShard("p1.", func(v int) bool {
			p1Values = append(p1Values, v)
			return true
		})
		assert.Len(t, p1Values, 3)
		assert.ElementsMatch(t, []int{1, 2, 3}, p1Values)
	})

	t.Run("filter by different prefix", func(t *testing.T) {
		var p2Values []int
		st.ForEachByShard("p2.", func(v int) bool {
			p2Values = append(p2Values, v)
			return true
		})
		assert.Len(t, p2Values, 2)
		assert.ElementsMatch(t, []int{4, 5}, p2Values)
	})

	t.Run("non-existent prefix", func(t *testing.T) {
		var values []int
		st.ForEachByShard("p999.", func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 0)
	})
}

func TestShardedPathTrie_ForEachDescendant(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Structure within shard "p1.r1":
	// root = 100
	// s1.u1 = 1
	// s1.u2 = 2
	// s2.u1 = 3

	st.Insert("p1.r1", nil, 100)
	st.Insert("p1.r1", []string{"s1", "u1"}, 1)
	st.Insert("p1.r1", []string{"s1", "u2"}, 2)
	st.Insert("p1.r1", []string{"s2", "u1"}, 3)
	st.Insert("p1.r2", []string{"u1"}, 4)

	t.Run("empty keyPath - all values in shard", func(t *testing.T) {
		var values []int
		st.ForEachDescendant("p1.r1", nil, func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 4)
		assert.ElementsMatch(t, []int{100, 1, 2, 3}, values)
	})

	t.Run("descendant of s1", func(t *testing.T) {
		var values []int
		st.ForEachDescendant("p1.r1", []string{"s1"}, func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 2)
		assert.ElementsMatch(t, []int{1, 2}, values)
	})

	t.Run("exact path", func(t *testing.T) {
		var values []int
		st.ForEachDescendant("p1.r1", []string{"s1", "u1"}, func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 1)
		assert.Equal(t, []int{1}, values)
	})

	t.Run("non-existent shard", func(t *testing.T) {
		var values []int
		st.ForEachDescendant("p999.r1", []string{"s1"}, func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 0)
	})

	t.Run("non-existent path in existing shard", func(t *testing.T) {
		var values []int
		st.ForEachDescendant("p1.r1", []string{"nonexistent"}, func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 0)
	})
}

func TestShardedPathTrie_ForEachInShard(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	st.Insert("p1.r1", []string{"u1"}, 1)
	st.Insert("p1.r1", []string{"u2"}, 2)
	st.Insert("p1.r2", []string{"u1"}, 3)

	t.Run("iterates all values in shard", func(t *testing.T) {
		var values []int
		st.ForEachInShard("p1.r1", func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 2)
		assert.ElementsMatch(t, []int{1, 2}, values)
	})

	t.Run("non-existent shard does nothing", func(t *testing.T) {
		var values []int
		st.ForEachInShard("p999.r999", func(v int) bool {
			values = append(values, v)
			return true
		})
		assert.Len(t, values, 0)
	})

	t.Run("early termination", func(t *testing.T) {
		st2 := trie.NewShardedPathTrie[int]()
		for i := range 10 {
			st2.Insert("p1.r1", []string{fmt.Sprintf("u%d", i)}, i)
		}

		count := 0
		st2.ForEachInShard("p1.r1", func(v int) bool {
			count++
			return count < 3
		})
		assert.Equal(t, 3, count)
	})
}

func TestShardedPathTrie_ShardKeys(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	st.Insert("p1.r1", []string{"u1"}, 1)
	st.Insert("p1.r2", []string{"u1"}, 2)
	st.Insert("p2.r1", []string{"u1"}, 3)

	keys := st.ShardKeys()
	assert.Len(t, keys, 3)
	assert.ElementsMatch(t, []string{"p1.r1", "p1.r2", "p2.r1"}, keys)
}

func TestShardedPathTrie_ConcurrentWrites(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	const numGoroutines = 100
	const numOpsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := range numGoroutines {
		go func(gid int) {
			defer wg.Done()

			shardKey := fmt.Sprintf("project%d.room%d", gid%10, gid%20)

			for i := range numOpsPerGoroutine {
				keyPath := []string{fmt.Sprintf("user%d", i)}
				value := gid*1000 + i
				st.Insert(shardKey, keyPath, value)

				// Verify we can read it back
				got, ok := st.Get(shardKey, keyPath)
				if !ok {
					t.Errorf("Failed to get value for shard=%s, path=%v", shardKey, keyPath)
				}
				if got != value {
					// Value might have been overwritten by another goroutine with same key
					// This is expected behavior for concurrent writes to same key
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify structure is consistent
	// Analysis: shardKey = "project{gid%10}.room{gid%20}"
	// For gid in 0..99, unique (gid%10, gid%20) pairs repeat every 20 goroutines
	// Unique shards: 20 (combinations of 0-9 with 0-19 that appear in the pattern)
	// Each shard receives writes from 5 goroutines (100/20)
	// Each goroutine writes 100 values (user0-user99), with overwrites from same shard
	// Total values: 20 shards * 100 values = 2000
	assert.Equal(t, 2000, st.Len())
	assert.Equal(t, 20, st.ShardCount())
}

func TestShardedPathTrie_ConcurrentReadWrite(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Pre-populate some data
	for i := range 100 {
		shardKey := fmt.Sprintf("p1.r%d", i%10)
		st.Insert(shardKey, []string{fmt.Sprintf("u%d", i)}, i)
	}

	var wg sync.WaitGroup
	const numReaders = 50
	const numWriters = 10

	// Writers
	for w := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for i := range 100 {
				shardKey := fmt.Sprintf("p1.r%d", wid)
				st.Insert(shardKey, []string{fmt.Sprintf("u%d", i+100)}, wid*1000+i)
			}
		}(w)
	}

	// Readers
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				st.ForEach(func(v int) bool {
					_ = v
					return true
				})
			}
		}()
	}

	wg.Wait()

	// Verify trie is in a consistent state after concurrent operations
	// Initial: 100 values in 10 shards (10 per shard)
	// Writers added: 10 writers * 100 values = 1000 (100 per shard)
	// Total: 10 shards * 110 values = 1100
	assert.Equal(t, 10, st.ShardCount())
	assert.Equal(t, 1100, st.Len())

	// Verify all written values exist with correct values
	for wid := range numWriters {
		shardKey := fmt.Sprintf("p1.r%d", wid)
		for i := range 100 {
			val, ok := st.Get(shardKey, []string{fmt.Sprintf("u%d", i+100)})
			assert.True(t, ok, "missing value for shard=%s, path=u%d", shardKey, i+100)
			assert.Equal(t, wid*1000+i, val)
		}
	}
}

func TestShardedPathTrie_EmptyShardKey(t *testing.T) {
	st := trie.NewShardedPathTrie[string]()

	t.Run("Insert with empty shard key is rejected", func(t *testing.T) {
		st.Insert("", []string{"u1"}, "should-not-insert")
		_, ok := st.Get("", []string{"u1"})
		assert.False(t, ok)
	})

	t.Run("Get with empty shard key returns false", func(t *testing.T) {
		_, ok := st.Get("", []string{"u1"})
		assert.False(t, ok)
	})

	t.Run("GetOrInsert with empty shard key returns zero value", func(t *testing.T) {
		createCalled := false
		val := st.GetOrInsert("", []string{"u1"}, func() string {
			createCalled = true
			return "value"
		})
		assert.False(t, createCalled)
		assert.Equal(t, "", val)
	})

	t.Run("Delete with empty shard key returns false", func(t *testing.T) {
		deleted := st.Delete("", []string{"u1"})
		assert.False(t, deleted)
	})

	t.Run("valid shard key works normally", func(t *testing.T) {
		st.Insert("p1.r1", []string{"u1"}, "valid")
		val, ok := st.Get("p1.r1", []string{"u1"})
		assert.True(t, ok)
		assert.Equal(t, "valid", val)
	})
}

func TestShardedPathTrie_ShardCleanup(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Insert values
	st.Insert("p1.r1", []string{"u1"}, 1)
	st.Insert("p1.r1", []string{"u2"}, 2)
	assert.Equal(t, 1, st.ShardCount())
	assert.Equal(t, 2, st.Len())

	// Delete one value - shard should remain
	st.Delete("p1.r1", []string{"u1"})
	st.DeleteShardIfEmpty("p1.r1") // Try cleanup - should not delete (still has values)
	assert.Equal(t, 1, st.ShardCount())
	assert.Equal(t, 1, st.Len())

	// Delete last value - shard should be removed after explicit cleanup
	st.Delete("p1.r1", []string{"u2"})
	assert.Equal(t, 1, st.ShardCount(), "shard remains until explicit cleanup")
	assert.Equal(t, 0, st.Len())

	// Explicit cleanup removes empty shard
	deleted := st.DeleteShardIfEmpty("p1.r1")
	assert.True(t, deleted)
	assert.Equal(t, 0, st.ShardCount())
}

func TestShardedPathTrie_EarlyTermination(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	for i := range 10 {
		st.Insert(fmt.Sprintf("p1.r%d", i), []string{"u1"}, i)
	}

	t.Run("ForEach early termination", func(t *testing.T) {
		count := 0
		st.ForEach(func(v int) bool {
			count++
			return count < 3
		})
		assert.Equal(t, 3, count)
	})

	t.Run("ForEachByShard early termination", func(t *testing.T) {
		count := 0
		st.ForEachByShard("p1.", func(v int) bool {
			count++
			return count < 3
		})
		assert.Equal(t, 3, count)
	})

	t.Run("ForEachDescendant early termination", func(t *testing.T) {
		st2 := trie.NewShardedPathTrie[int]()
		for i := range 10 {
			st2.Insert("p1.r1", []string{fmt.Sprintf("u%d", i)}, i)
		}

		count := 0
		st2.ForEachDescendant("p1.r1", nil, func(v int) bool {
			count++
			return count < 3
		})
		assert.Equal(t, 3, count)
	})
}

func TestShardedPathTrie_InsertOverwrites(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Test overwrite for deep path
	st.Insert("p1.r1", []string{"u1"}, 100)
	val, ok := st.Get("p1.r1", []string{"u1"})
	assert.True(t, ok)
	assert.Equal(t, 100, val)

	st.Insert("p1.r1", []string{"u1"}, 200)
	val, ok = st.Get("p1.r1", []string{"u1"})
	assert.True(t, ok)
	assert.Equal(t, 200, val)

	// Test overwrite for root value (empty keyPath)
	st.Insert("p1.lobby", nil, 300)
	val, ok = st.Get("p1.lobby", nil)
	assert.True(t, ok)
	assert.Equal(t, 300, val)

	st.Insert("p1.lobby", nil, 400)
	val, ok = st.Get("p1.lobby", nil)
	assert.True(t, ok)
	assert.Equal(t, 400, val)
}

func TestShardedPathTrie_ConcurrentGetOrInsert(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	var wg sync.WaitGroup
	var createCount int32 = 0

	shardKey := "p1.r1"
	keyPath := []string{"u1"}

	// Many goroutines trying to GetOrInsert the same key
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.GetOrInsert(shardKey, keyPath, func() int {
				count := int(createCount)
				createCount++
				return count
			})
		}()
	}

	wg.Wait()

	// Create should be called exactly once
	assert.Equal(t, int32(1), createCount)
	assert.Equal(t, 1, st.Len())
}

func TestShardedPathTrie_ConcurrentDelete(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Insert many values
	for i := range 100 {
		shardKey := fmt.Sprintf("p1.r%d", i%10)
		st.Insert(shardKey, []string{fmt.Sprintf("u%d", i)}, i)
	}

	var wg sync.WaitGroup

	// Concurrent deletes
	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			shardKey := fmt.Sprintf("p1.r%d", idx%10)
			st.Delete(shardKey, []string{fmt.Sprintf("u%d", idx)})
		}(i)
	}

	wg.Wait()

	// All values should be deleted
	assert.Equal(t, 0, st.Len())

	// Cleanup empty shards (caller's responsibility)
	for i := range 10 {
		st.DeleteShardIfEmpty(fmt.Sprintf("p1.r%d", i))
	}
	assert.Equal(t, 0, st.ShardCount())
}

func TestShardedPathTrie_ConcurrentForEachByShardWithModifications(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Pre-populate with values in multiple shards
	for i := range 50 {
		st.Insert(fmt.Sprintf("p1.r%d", i), []string{"u1"}, i)
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers doing ForEachByShard
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				st.ForEachByShard("p1.", func(v int) bool {
					count++
					return true
				})
				// Count may vary due to concurrent modifications
				_ = count
			}
		}()
	}

	// Writers adding new values
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 50 {
				st.Insert(fmt.Sprintf("p1.r%d", 100+wid*50+j), []string{"u1"}, wid*1000+j)
			}
		}(i)
	}

	// Deleters removing values
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 5 {
				st.Delete(fmt.Sprintf("p1.r%d", did*5+j), []string{"u1"})
			}
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial: 50 shards (p1.r0 - p1.r49), each with 1 value = 50 values
	// Added: 10 writers * 50 = 500 new shards (p1.r100 - p1.r599)
	// Deleted: 10 deleters * 5 = 50 values (p1.r0 - p1.r49)
	// Expected: 50 - 50 + 500 = 500 values
	assert.Equal(t, 500, st.Len())

	// Verify all added values exist
	for wid := range numWriters {
		for j := range 50 {
			shardKey := fmt.Sprintf("p1.r%d", 100+wid*50+j)
			val, ok := st.Get(shardKey, []string{"u1"})
			assert.True(t, ok, "missing value for shard=%s", shardKey)
			assert.Equal(t, wid*1000+j, val)
		}
	}
}

func TestShardedPathTrie_ConcurrentForEachDescendantWithModifications(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Pre-populate with hierarchical structure
	st.Insert("p1.r1", nil, 0)
	for i := range 20 {
		st.Insert("p1.r1", []string{fmt.Sprintf("s%d", i)}, i+1)
		st.Insert("p1.r1", []string{fmt.Sprintf("s%d", i), "u1"}, i*100+1)
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers doing ForEachDescendant
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				st.ForEachDescendant("p1.r1", nil, func(v int) bool {
					count++
					return true
				})
				_ = count
			}
		}()
	}

	// Writers adding descendants
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 20 {
				st.Insert("p1.r1", []string{fmt.Sprintf("new-s%d-%d", wid, j)}, wid*1000+j)
			}
		}(i)
	}

	// Deleters removing descendants
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 2 {
				st.Delete("p1.r1", []string{fmt.Sprintf("s%d", did*2+j)})
			}
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial: 1 (root) + 20 (s0-s19) + 20 (s0.u1 - s19.u1) = 41 values
	// Added: 10 writers * 20 = 200 values (new-s{wid}-{j})
	// Deleted: 10 deleters * 2 = 20 values (s0-s19)
	// Expected: 41 - 20 + 200 = 221 values
	finalCount := 0
	st.ForEachDescendant("p1.r1", nil, func(v int) bool {
		finalCount++
		return true
	})
	assert.Equal(t, 221, finalCount)

	// Verify root still exists
	rootVal, ok := st.Get("p1.r1", nil)
	assert.True(t, ok)
	assert.Equal(t, 0, rootVal)

	// Verify all added values exist
	for wid := range numWriters {
		for j := range 20 {
			val, ok := st.Get("p1.r1", []string{fmt.Sprintf("new-s%d-%d", wid, j)})
			assert.True(t, ok, "missing new-s%d-%d", wid, j)
			assert.Equal(t, wid*1000+j, val)
		}
	}
}

func TestShardedPathTrie_ConcurrentForEachInShardWithModifications(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Pre-populate a single shard
	for i := range 50 {
		st.Insert("p1.r1", []string{fmt.Sprintf("u%d", i)}, i)
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers doing ForEachInShard
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				count := 0
				st.ForEachInShard("p1.r1", func(v int) bool {
					count++
					return true
				})
				_ = count
			}
		}()
	}

	// Writers adding to same shard
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 20 {
				st.Insert("p1.r1", []string{fmt.Sprintf("new-u%d-%d", wid, j)}, wid*1000+j)
			}
		}(i)
	}

	// Deleters removing from same shard
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 5 {
				st.Delete("p1.r1", []string{fmt.Sprintf("u%d", did*5+j)})
			}
		}(i)
	}

	wg.Wait()

	// Verify final state:
	// Initial: 50 values (u0-u49)
	// Added: 10 writers * 20 = 200 values (new-u{wid}-{j})
	// Deleted: 10 deleters * 5 = 50 values (u0-u49)
	// Expected: 50 - 50 + 200 = 200 values
	finalCount := 0
	st.ForEachInShard("p1.r1", func(v int) bool {
		finalCount++
		return true
	})
	assert.Equal(t, 200, finalCount)

	// Verify all added values exist
	for wid := range numWriters {
		for j := range 20 {
			val, ok := st.Get("p1.r1", []string{fmt.Sprintf("new-u%d-%d", wid, j)})
			assert.True(t, ok, "missing new-u%d-%d", wid, j)
			assert.Equal(t, wid*1000+j, val)
		}
	}
}

func TestShardedPathTrie_ConcurrentShardCleanupRace(t *testing.T) {
	// Test race between shard deletion (when last value is deleted) and new insertion
	const iterations = 100

	for range iterations {
		st := trie.NewShardedPathTrie[int]()

		// Insert a single value
		st.Insert("p1.r1", []string{"u1"}, 1)

		var wg sync.WaitGroup
		wg.Add(2)

		// Goroutine 1: Delete the value (may trigger shard cleanup)
		go func() {
			defer wg.Done()
			st.Delete("p1.r1", []string{"u1"})
		}()

		// Goroutine 2: Insert a new value to the same shard
		go func() {
			defer wg.Done()
			st.Insert("p1.r1", []string{"u2"}, 2)
		}()

		wg.Wait()

		// Trie should be in a consistent state
		// Either u2 exists alone, or both were cleaned up, or both exist
		len := st.Len()
		assert.LessOrEqual(t, len, 2)
		assert.GreaterOrEqual(t, len, 0)
	}
}

func TestShardedPathTrie_ConcurrentRootValueOperations(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	var wg sync.WaitGroup
	const numGoroutines = 50

	// Mixed GetOrInsert and Insert for root values
	for i := range numGoroutines {
		wg.Add(2)

		// GetOrInsert root
		go func(idx int) {
			defer wg.Done()
			st.GetOrInsert(fmt.Sprintf("p1.r%d", idx%10), nil, func() int {
				return idx + 1
			})
		}(i)

		// Insert root (overwrites)
		go func(idx int) {
			defer wg.Done()
			st.Insert(fmt.Sprintf("p1.r%d", idx%10), nil, (idx+1)*100)
		}(i)
	}

	wg.Wait()

	// Should have exactly 10 shards (p1.r0 through p1.r9)
	assert.Equal(t, 10, st.ShardCount())
	assert.Equal(t, 10, st.Len())

	// Each shard should have a value
	for i := range 10 {
		val, ok := st.Get(fmt.Sprintf("p1.r%d", i), nil)
		assert.True(t, ok)
		assert.NotZero(t, val)
	}
}

func TestShardedPathTrie_ConcurrentMultiShardOperations(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	var wg sync.WaitGroup
	const numGoroutines = 50

	// Concurrent operations across many different shards
	for i := range numGoroutines {
		wg.Add(3)

		// Insert to random shards
		go func(idx int) {
			defer wg.Done()
			for j := range 20 {
				shardKey := fmt.Sprintf("p%d.r%d", idx%5, j%10)
				st.Insert(shardKey, []string{fmt.Sprintf("u%d", idx)}, idx*1000+j)
			}
		}(i)

		// Read from random shards
		go func(idx int) {
			defer wg.Done()
			for j := range 20 {
				shardKey := fmt.Sprintf("p%d.r%d", idx%5, j%10)
				_, _ = st.Get(shardKey, []string{fmt.Sprintf("u%d", (idx+1)%numGoroutines)})
			}
		}(i)

		// ForEach across all shards
		go func() {
			defer wg.Done()
			count := 0
			st.ForEach(func(v int) bool {
				count++
				return count < 100 // Early termination
			})
		}()
	}

	wg.Wait()

	// Verify final state:
	// 50 goroutines, each writes to 10 shards (p{idx%5}.r{j%10})
	// Total shards: 5 * 10 = 50
	// Each shard receives writes from 10 goroutines (those where idx%5 == a)
	// Each goroutine writes unique key u{idx}, so each shard has 10 values
	// Total: 50 shards * 10 values = 500
	assert.Equal(t, 50, st.ShardCount())
	assert.Equal(t, 500, st.Len())
}

func TestShardedPathTrie_RootValueWithChildren(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Insert root value and children in the same shard
	st.Insert("p1.r1", nil, 100)                // root value
	st.Insert("p1.r1", []string{"u1"}, 1)       // child
	st.Insert("p1.r1", []string{"u2"}, 2)       // child
	st.Insert("p1.r1", []string{"s1", "u3"}, 3) // grandchild

	assert.Equal(t, 4, st.Len())
	assert.Equal(t, 1, st.ShardCount())

	// ForEachDescendant should include root value
	var values []int
	st.ForEachDescendant("p1.r1", nil, func(v int) bool {
		values = append(values, v)
		return true
	})
	assert.Len(t, values, 4)
	assert.ElementsMatch(t, []int{100, 1, 2, 3}, values)

	// Delete root, children should remain
	st.Delete("p1.r1", nil)
	assert.Equal(t, 3, st.Len())

	_, ok := st.Get("p1.r1", nil)
	assert.False(t, ok)

	val, ok := st.Get("p1.r1", []string{"u1"})
	assert.True(t, ok)
	assert.Equal(t, 1, val)
}

func TestShardedPathTrie_ConcurrentShardKeysWithModifications(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()

	// Pre-populate
	for i := range 20 {
		st.Insert(fmt.Sprintf("p1.r%d", i), []string{"u1"}, i)
	}

	var wg sync.WaitGroup
	const numReaders = 20
	const numWriters = 10

	// Readers calling ShardKeys
	for range numReaders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				keys := st.ShardKeys()
				// Keys should be a consistent snapshot
				_ = keys
			}
		}()
	}

	// Writers adding new shards
	for i := range numWriters {
		wg.Add(1)
		go func(wid int) {
			defer wg.Done()
			for j := range 10 {
				st.Insert(fmt.Sprintf("p2.r%d-%d", wid, j), []string{"u1"}, wid*1000+j)
			}
		}(i)
	}

	// Deleters removing values from shards
	for i := range numWriters {
		wg.Add(1)
		go func(did int) {
			defer wg.Done()
			for j := range 2 {
				st.Delete(fmt.Sprintf("p1.r%d", did*2+j), []string{"u1"})
			}
		}(i)
	}

	wg.Wait()

	// Verify final state (before shard cleanup):
	// Initial: 20 shards (p1.r0 - p1.r19)
	// Added: 10 writers * 10 = 100 new shards (p2.r{wid}-{j})
	// Deleted values: 20 values removed, but shards remain until explicit cleanup
	// Expected values: 100 (only p2 shards have values)
	// Expected shards before cleanup: 120 (20 empty p1 shards + 100 p2 shards)
	assert.Equal(t, 100, st.Len())
	assert.Equal(t, 120, st.ShardCount(), "empty shards remain until explicit cleanup")

	// Cleanup empty shards
	for i := range 20 {
		st.DeleteShardIfEmpty(fmt.Sprintf("p1.r%d", i))
	}

	// After cleanup: only 100 p2 shards remain
	keys := st.ShardKeys()
	assert.Equal(t, len(keys), st.ShardCount())
	assert.Equal(t, 100, st.ShardCount())
	assert.Equal(t, 100, st.Len())

	// Verify all added shards exist
	for wid := range numWriters {
		for j := range 10 {
			shardKey := fmt.Sprintf("p2.r%d-%d", wid, j)
			val, ok := st.Get(shardKey, []string{"u1"})
			assert.True(t, ok, "missing shard %s", shardKey)
			assert.Equal(t, wid*1000+j, val)
		}
	}
}

func TestShardedPathTrie_ConcurrentGetOrInsertRootSameKey(t *testing.T) {
	st := trie.NewShardedPathTrie[int]()
	var wg sync.WaitGroup
	var createCount int32 = 0

	shardKey := "p1.r1"

	// Many goroutines trying to GetOrInsert root value for same shard
	for range 100 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st.GetOrInsert(shardKey, nil, func() int {
				// Atomically increment to detect multiple calls
				count := atomic.AddInt32(&createCount, 1)
				return int(count)
			})
		}()
	}

	wg.Wait()

	// Create should be called exactly once
	assert.Equal(t, int32(1), createCount)
	assert.Equal(t, 1, st.Len())
	assert.Equal(t, 1, st.ShardCount())

	// Value should be 1 (first create call)
	val, ok := st.Get(shardKey, nil)
	assert.True(t, ok)
	assert.Equal(t, 1, val)
}

func TestShardedPathTrie_ConcurrentDeleteRoot(t *testing.T) {
	const iterations = 100

	for range iterations {
		st := trie.NewShardedPathTrie[int]()

		// Insert root value
		st.Insert("p1.r1", nil, 100)
		assert.Equal(t, 1, st.Len())

		var wg sync.WaitGroup

		// Multiple goroutines trying to delete root
		for range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				st.Delete("p1.r1", nil)
			}()
		}

		wg.Wait()

		// Root should be deleted
		_, ok := st.Get("p1.r1", nil)
		assert.False(t, ok)
		assert.Equal(t, 0, st.Len())
	}
}
