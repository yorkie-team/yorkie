/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package cache provides cache implementations with statistics tracking.
package cache

import (
	"fmt"
	"hash/fnv"
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

const numShards = 16

// LRU is a sharded LRU cache wrapper that tracks hit/miss statistics.
// Keys are distributed across multiple shards to reduce lock contention.
type LRU[K comparable, V any] struct {
	shards [numShards]*lru.Cache[K, V]
	stats  *Stats
	name   string
}

// NewLRU creates a new sharded LRU cache with statistics tracking.
func NewLRU[K comparable, V any](size int, name string) (*LRU[K, V], error) {
	perShard := size / numShards
	if perShard < 1 {
		perShard = 1
	}

	c := &LRU[K, V]{
		stats: &Stats{},
		name:  name,
	}

	for i := 0; i < numShards; i++ {
		shard, err := lru.New[K, V](perShard)
		if err != nil {
			return nil, err
		}
		c.shards[i] = shard
	}

	return c, nil
}

// shard returns the shard index for the given key.
func (c *LRU[K, V]) shard(key K) int {
	h := fnv.New32a()
	_, _ = fmt.Fprintf(h, "%v", key)
	return int(h.Sum32() & (numShards - 1))
}

// Get retrieves a value from the cache and updates statistics.
func (c *LRU[K, V]) Get(key K) (V, bool) {
	value, ok := c.shards[c.shard(key)].Get(key)
	if ok {
		atomic.AddInt64(&c.stats.hits, 1)
	} else {
		atomic.AddInt64(&c.stats.misses, 1)
	}
	return value, ok
}

// Add adds a value to the cache.
func (c *LRU[K, V]) Add(key K, value V) bool {
	return c.shards[c.shard(key)].Add(key, value)
}

// Contains checks if a key exists in the cache without updating statistics.
func (c *LRU[K, V]) Contains(key K) bool {
	return c.shards[c.shard(key)].Contains(key)
}

// Peek retrieves a value from the cache without updating LRU or statistics.
func (c *LRU[K, V]) Peek(key K) (V, bool) {
	return c.shards[c.shard(key)].Peek(key)
}

// Remove removes a key from the cache.
func (c *LRU[K, V]) Remove(key K) bool {
	return c.shards[c.shard(key)].Remove(key)
}

// Purge clears all entries from the cache.
func (c *LRU[K, V]) Purge() {
	for i := 0; i < numShards; i++ {
		c.shards[i].Purge()
	}
}

// Len returns the number of items in the cache.
func (c *LRU[K, V]) Len() int {
	n := 0
	for i := 0; i < numShards; i++ {
		n += c.shards[i].Len()
	}
	return n
}

// Stats returns the cache statistics.
func (c *LRU[K, V]) Stats() *Stats {
	return c.stats
}

// Name returns the cache name.
func (c *LRU[K, V]) Name() string {
	return c.name
}
