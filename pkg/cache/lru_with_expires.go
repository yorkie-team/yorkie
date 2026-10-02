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
	"errors"
	"fmt"
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

// MinTTL is the smallest TTL an expirable LRU can safely be given. The
// underlying implementation starts an expiry ticker at TTL / 100, so MinTTL
// keeps that ticker at 1ms or slower: a shorter TTL wakes the ticker tens of
// thousands of times a second for the cache's lifetime, and one below 100ns
// truncates the interval to zero and panics. A non-positive TTL is not a safe
// alternative either: it is reinterpreted as "never expire" (~10 years), which
// turns the cache into a store entries never leave.
const MinTTL = 100 * time.Millisecond

// ErrInvalidTTL is returned when an expirable LRU is given a TTL below MinTTL.
var ErrInvalidTTL = errors.New("cache TTL is below the minimum")

// ParseTTL parses value as a duration and checks that it is a TTL an
// expirable LRU accepts. It returns ErrInvalidTTL if the TTL is below MinTTL.
func ParseTTL(value string) (time.Duration, error) {
	ttl, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse cache TTL: %w", err)
	}
	if ttl < MinTTL {
		return 0, fmt.Errorf("%w %s", ErrInvalidTTL, MinTTL)
	}
	return ttl, nil
}

// NewLRUWithExpires creates a new expirable LRU with the given size and ttl.
// It returns ErrInvalidTTL if ttl is less than MinTTL.
func NewLRUWithExpires[K comparable, V any](
	size int,
	ttl time.Duration,
	name string,
	onEvict ...func(key K, value V),
) (*LRUWithExpires[K, V], error) {
	if ttl < MinTTL {
		return nil, fmt.Errorf("%s cache TTL %s: %w %s", name, ttl, ErrInvalidTTL, MinTTL)
	}

	var callback func(key K, value V)
	if len(onEvict) > 0 {
		callback = onEvict[0]
	}

	c := expirable.NewLRU(size, callback, ttl)
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
		c.stats.hits.Add(1)
	} else {
		c.stats.misses.Add(1)
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

// RemoveIf removes every entry whose key satisfies the predicate and returns
// the number of entries removed.
func (c *LRUWithExpires[K, V]) RemoveIf(pred func(K) bool) int {
	removed := 0
	for _, key := range c.cache.Keys() {
		if pred(key) && c.cache.Remove(key) {
			removed++
		}
	}
	return removed
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
