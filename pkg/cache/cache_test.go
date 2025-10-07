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

package cache_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

func TestLRUWithStats(t *testing.T) {
	t.Run("basic operations", func(t *testing.T) {
		c, err := cache.NewLRUWithStats[string, int](3, "test-cache")
		assert.NoError(t, err)

		// Test cache miss
		_, ok := c.Get("key1")
		assert.False(t, ok)
		assert.Equal(t, int64(0), c.Stats().Hits())
		assert.Equal(t, int64(1), c.Stats().Misses())

		// Add and test cache hit
		c.Add("key1", 100)
		val, ok := c.Get("key1")
		assert.True(t, ok)
		assert.Equal(t, 100, val)
		assert.Equal(t, int64(1), c.Stats().Hits())
		assert.Equal(t, int64(1), c.Stats().Misses())

		// Test hit rate
		assert.Equal(t, 50.0, c.Stats().HitRate())
	})

	t.Run("hit rate calculation", func(t *testing.T) {
		c, err := cache.NewLRUWithStats[string, int](5, "test-cache")
		assert.NoError(t, err)

		// Add some data
		c.Add("key1", 1)
		c.Add("key2", 2)
		c.Add("key3", 3)

		// Generate hits and misses
		c.Get("key1") // hit
		c.Get("key2") // hit
		c.Get("key3") // hit
		c.Get("key4") // miss
		c.Get("key5") // miss

		assert.Equal(t, int64(3), c.Stats().Hits())
		assert.Equal(t, int64(2), c.Stats().Misses())
		assert.Equal(t, int64(5), c.Stats().Total())
		assert.Equal(t, 60.0, c.Stats().HitRate())
	})
}

func TestCacheManager(t *testing.T) {
	t.Run("register and log stats", func(t *testing.T) {
		manager := cache.NewManager(time.Second)

		cache1, err := cache.NewLRUWithStats[string, int](5, "cache1")
		assert.NoError(t, err)
		cache2, err := cache.NewLRUWithStats[string, string](5, "cache2")
		assert.NoError(t, err)

		manager.RegisterCache(cache1)
		manager.RegisterCache(cache2)

		// Add some test data
		cache1.Add("key1", 1)
		cache1.Get("key1") // hit
		cache1.Get("key2") // miss

		cache2.Add("key1", "value1")
		cache2.Get("key1") // hit
		cache2.Get("key2") // miss

		// This should log the stats
		manager.LogCacheStats()
	})
}
