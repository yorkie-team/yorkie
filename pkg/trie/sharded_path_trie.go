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
	"strings"

	"github.com/yorkie-team/yorkie/pkg/cmap"
)

// ShardedPathTrie is a generic sharded trie that delegates sharding strategy to the caller.
// The caller provides the shard key and remaining path for each operation.
//
// This design allows:
//   - Different sharding strategies for different use cases
//   - Complete separation from domain-specific logic
//   - Reusability across different domains
//
// Example usage:
//
//	st := NewShardedPathTrie[*MyValue]()
//	shardKey := "tenant-1.room-1"     // Caller determines shard key
//	keyPath := []string{"user", "1"}  // Remaining path within shard
//	st.Insert(shardKey, keyPath, myValue)
type ShardedPathTrie[T any] struct {
	shards *cmap.Map[string, *PathTrie[T]]
}

// NewShardedPathTrie creates a new sharded path trie.
func NewShardedPathTrie[T any]() *ShardedPathTrie[T] {
	return &ShardedPathTrie[T]{
		shards: cmap.New[string, *PathTrie[T]](),
	}
}

// getOrCreateShard returns the shard for the given key, creating it if necessary.
func (st *ShardedPathTrie[T]) getOrCreateShard(shardKey string) *PathTrie[T] {
	return st.shards.Upsert(shardKey, func(existing *PathTrie[T], exists bool) *PathTrie[T] {
		if exists {
			return existing
		}
		return NewPathTrie[T]()
	})
}

// Insert adds or updates a value in the trie.
// shardKey determines which shard to use.
// keyPath is the path within the shard (can be empty for root value).
func (st *ShardedPathTrie[T]) Insert(shardKey string, keyPath []string, value T) {
	if shardKey == "" {
		return
	}

	shard := st.getOrCreateShard(shardKey)

	if len(keyPath) == 0 {
		shard.InsertRoot(value)
		return
	}

	shard.Insert(keyPath, value)
}

// Get retrieves a value by shard key and path.
// This operation is lock-free.
func (st *ShardedPathTrie[T]) Get(shardKey string, keyPath []string) (T, bool) {
	var zero T
	if shardKey == "" {
		return zero, false
	}

	shard, ok := st.shards.Get(shardKey)
	if !ok {
		return zero, false
	}

	if len(keyPath) == 0 {
		return shard.GetRoot()
	}
	return shard.Get(keyPath)
}

// GetOrInsert atomically retrieves a value or inserts a new one if it doesn't exist.
// The create function is called only if the value doesn't exist.
func (st *ShardedPathTrie[T]) GetOrInsert(shardKey string, keyPath []string, create func() T) T {
	var zero T
	if shardKey == "" {
		return zero
	}

	shard := st.getOrCreateShard(shardKey)

	if len(keyPath) == 0 {
		return shard.GetOrInsertRoot(create)
	}
	return shard.GetOrInsert(keyPath, create)
}

// Delete removes a value from the trie.
// Returns true if a value was deleted, false if the path didn't exist.
// Note: Empty shard cleanup is the caller's responsibility to avoid race conditions.
// The caller should use DeleteEmptyShard() or handle cleanup at a higher level with proper locking.
func (st *ShardedPathTrie[T]) Delete(shardKey string, keyPath []string) bool {
	if shardKey == "" {
		return false
	}

	shard, ok := st.shards.Get(shardKey)
	if !ok {
		return false
	}

	if len(keyPath) == 0 {
		return shard.DeleteRoot()
	}
	return shard.Delete(keyPath)
}

// DeleteShardIfEmpty removes a shard only if it's empty.
// This should be called by the caller after Delete() when appropriate,
// typically while holding a higher-level lock to prevent race conditions.
// Returns true if the shard was deleted, false otherwise.
func (st *ShardedPathTrie[T]) DeleteShardIfEmpty(shardKey string) bool {
	if shardKey == "" {
		return false
	}

	return st.shards.Delete(shardKey, func(shard *PathTrie[T], exists bool) bool {
		return exists && shard.Len() == 0
	})
}

// ForEach traverses all values in all shards.
// This operation is lock-free and can run concurrently with writes.
func (st *ShardedPathTrie[T]) ForEach(fn func(T) bool) {
	for _, shard := range st.shards.Values() {
		shouldContinue := true
		shard.ForEach(func(value T) bool {
			shouldContinue = fn(value)
			return shouldContinue
		})
		if !shouldContinue {
			return
		}
	}
}

// ForEachByShard traverses all values in shards whose key starts with the given prefix.
// This is useful for grouping values by a common shard key prefix.
func (st *ShardedPathTrie[T]) ForEachByShard(shardKeyPrefix string, fn func(T) bool) {
	for _, shardKey := range st.shards.Keys() {
		if !strings.HasPrefix(shardKey, shardKeyPrefix) {
			continue
		}

		shard, ok := st.shards.Get(shardKey)
		if !ok {
			continue
		}

		shouldContinue := true
		shard.ForEach(func(value T) bool {
			shouldContinue = fn(value)
			return shouldContinue
		})
		if !shouldContinue {
			return
		}
	}
}

// ForEachInShard traverses all values in a specific shard.
func (st *ShardedPathTrie[T]) ForEachInShard(shardKey string, fn func(T) bool) {
	shard, ok := st.shards.Get(shardKey)
	if !ok {
		return
	}
	shard.ForEach(fn)
}

// ForEachDescendant traverses descendant values within a shard.
func (st *ShardedPathTrie[T]) ForEachDescendant(shardKey string, keyPath []string, fn func(T) bool) {
	shard, ok := st.shards.Get(shardKey)
	if !ok {
		return
	}

	if len(keyPath) == 0 {
		shard.ForEach(fn)
		return
	}

	shard.ForEachDescendant(keyPath, fn)
}

// ShardKeys returns all shard keys currently in use.
// This is useful for iteration and debugging.
func (st *ShardedPathTrie[T]) ShardKeys() []string {
	return st.shards.Keys()
}

// Len returns the total number of values in the trie.
func (st *ShardedPathTrie[T]) Len() int {
	total := 0
	for _, shard := range st.shards.Values() {
		total += shard.Len()
	}
	return total
}

// ShardCount returns the number of shards currently in use.
// This is useful for monitoring and debugging.
func (st *ShardedPathTrie[T]) ShardCount() int {
	return st.shards.Len()
}
