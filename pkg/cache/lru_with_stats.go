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
	"sync/atomic"

	lru "github.com/hashicorp/golang-lru/v2"
)

// Stats holds cache statistics.
type Stats struct {
	hits   int64
	misses int64
}

// Hits returns the number of cache hits.
func (s *Stats) Hits() int64 {
	return atomic.LoadInt64(&s.hits)
}

// Misses returns the number of cache misses.
func (s *Stats) Misses() int64 {
	return atomic.LoadInt64(&s.misses)
}

// Total returns the total number of cache operations.
func (s *Stats) Total() int64 {
	return s.Hits() + s.Misses()
}

// HitRate returns the cache hit rate as a percentage (0-100).
func (s *Stats) HitRate() float64 {
	total := s.Total()
	if total == 0 {
		return 0.0
	}
	return float64(s.Hits()) / float64(total) * 100.0
}

// Reset resets all statistics to zero.
func (s *Stats) Reset() {
	atomic.StoreInt64(&s.hits, 0)
	atomic.StoreInt64(&s.misses, 0)
}

// LRUWithStats is an LRU cache wrapper that tracks hit/miss statistics.
type LRUWithStats[K comparable, V any] struct {
	cache *lru.Cache[K, V]
	stats *Stats
	name  string
}

// NewLRUWithStats creates a new LRU cache with statistics tracking.
func NewLRUWithStats[K comparable, V any](size int, name string) (*LRUWithStats[K, V], error) {
	cache, err := lru.New[K, V](size)
	if err != nil {
		return nil, err
	}

	return &LRUWithStats[K, V]{
		cache: cache,
		stats: &Stats{},
		name:  name,
	}, nil
}

// Get retrieves a value from the cache and updates statistics.
func (c *LRUWithStats[K, V]) Get(key K) (V, bool) {
	value, ok := c.cache.Get(key)
	if ok {
		atomic.AddInt64(&c.stats.hits, 1)
	} else {
		atomic.AddInt64(&c.stats.misses, 1)
	}
	return value, ok
}

// Add adds a value to the cache.
func (c *LRUWithStats[K, V]) Add(key K, value V) bool {
	return c.cache.Add(key, value)
}

// Contains checks if a key exists in the cache without updating statistics.
func (c *LRUWithStats[K, V]) Contains(key K) bool {
	return c.cache.Contains(key)
}

// Peek retrieves a value from the cache without updating LRU or statistics.
func (c *LRUWithStats[K, V]) Peek(key K) (V, bool) {
	return c.cache.Peek(key)
}

// Remove removes a key from the cache.
func (c *LRUWithStats[K, V]) Remove(key K) bool {
	return c.cache.Remove(key)
}

// Purge clears all entries from the cache.
func (c *LRUWithStats[K, V]) Purge() {
	c.cache.Purge()
}

// Len returns the number of items in the cache.
func (c *LRUWithStats[K, V]) Len() int {
	return c.cache.Len()
}

// Stats returns the cache statistics.
func (c *LRUWithStats[K, V]) Stats() *Stats {
	return c.stats
}

// Name returns the cache name.
func (c *LRUWithStats[K, V]) Name() string {
	return c.name
}
