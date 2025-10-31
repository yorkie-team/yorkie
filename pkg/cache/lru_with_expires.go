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

// Package cache provides cache implementations with expiration support.
package cache

import (
	"sync/atomic"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
)

// Note: Stats type and its helpers are defined in lru_with_stats.go; this
// file only provides an expirable LRU wrapper that uses that Stats type.

// LRUWithExpires is a wrapper over hashicorp's expirable LRU with statistics.
type LRUWithExpires[K comparable, V any] struct {
	cache *expirable.LRU[K, V]
	stats *Stats
	name  string
}

// NewLRUWithExpires creates a new expirable LRU with the given size and ttl.
func NewLRUWithExpires[K comparable, V any](size int, ttl time.Duration, name string) (*LRUWithExpires[K, V], error) {
	c := expirable.NewLRU[K, V](size, nil, ttl)
	return &LRUWithExpires[K, V]{
		cache: c,
		stats: &Stats{},
		name:  name,
	}, nil
}

// Get retrieves a value from the cache and updates statistics.
func (c *LRUWithExpires[K, V]) Get(key K) (V, bool) {
	value, ok := c.cache.Get(key)
	if ok {
		atomic.AddInt64(&c.stats.hits, 1)
	} else {
		atomic.AddInt64(&c.stats.misses, 1)
	}
	return value, ok
}

// Add adds a value to the cache.
func (c *LRUWithExpires[K, V]) Add(key K, value V) bool {
	return c.cache.Add(key, value)
}

// Contains checks if a key exists in the cache without updating statistics.
func (c *LRUWithExpires[K, V]) Contains(key K) bool {
	return c.cache.Contains(key)
}

// Peek retrieves a value from the cache without updating LRU or statistics.
func (c *LRUWithExpires[K, V]) Peek(key K) (V, bool) {
	return c.cache.Peek(key)
}

// Remove removes a key from the cache.
func (c *LRUWithExpires[K, V]) Remove(key K) bool {
	return c.cache.Remove(key)
}

// Purge clears all entries from the cache.
func (c *LRUWithExpires[K, V]) Purge() {
	c.cache.Purge()
}

// Len returns the number of items in the cache.
func (c *LRUWithExpires[K, V]) Len() int {
	return c.cache.Len()
}

// Stats returns the cache statistics.
func (c *LRUWithExpires[K, V]) Stats() *Stats {
	return c.stats
}

// Name returns the cache name.
func (c *LRUWithExpires[K, V]) Name() string {
	return c.name
}
