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

package prometheus_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/cache"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
)

var cacheMetricNames = []string{
	"yorkie_cache_hits_total",
	"yorkie_cache_misses_total",
}

func TestCacheMetrics(t *testing.T) {
	t.Run("report hits and misses test", func(t *testing.T) {
		metrics, err := prometheus.NewMetrics()
		assert.NoError(t, err)

		lru, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)
		assert.NoError(t, metrics.RegisterCaches("test-host", lru))

		// Nothing has been looked up yet, so both counters start at zero and
		// are reported rather than absent.
		assert.NoError(t, testutil.GatherAndCompare(metrics.Registry(), strings.NewReader(`
# HELP yorkie_cache_hits_total The total count of lookups that found an entry in the cache.
# TYPE yorkie_cache_hits_total counter
yorkie_cache_hits_total{cache="snapshots",hostname="test-host"} 0
# HELP yorkie_cache_misses_total The total count of lookups that found no entry in the cache.
# TYPE yorkie_cache_misses_total counter
yorkie_cache_misses_total{cache="snapshots",hostname="test-host"} 0
`), cacheMetricNames...))

		lru.Add("a", 1)
		_, ok := lru.Get("a")
		assert.True(t, ok)
		_, ok = lru.Get("a")
		assert.True(t, ok)
		_, ok = lru.Get("b")
		assert.False(t, ok)

		// The collector reads the cache at scrape time, so the counters follow
		// the lookups without the cache being told about the metrics.
		assert.NoError(t, testutil.GatherAndCompare(metrics.Registry(), strings.NewReader(`
# HELP yorkie_cache_hits_total The total count of lookups that found an entry in the cache.
# TYPE yorkie_cache_hits_total counter
yorkie_cache_hits_total{cache="snapshots",hostname="test-host"} 2
# HELP yorkie_cache_misses_total The total count of lookups that found no entry in the cache.
# TYPE yorkie_cache_misses_total counter
yorkie_cache_misses_total{cache="snapshots",hostname="test-host"} 1
`), cacheMetricNames...))
	})

	t.Run("report each registered cache separately test", func(t *testing.T) {
		metrics, err := prometheus.NewMetrics()
		assert.NoError(t, err)

		snapshots, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)
		webhooks, err := cache.NewLRUWithExpires[string, int](128, time.Minute, "auth-webhook")
		assert.NoError(t, err)
		assert.NoError(t, metrics.RegisterCaches("test-host", snapshots, webhooks))

		snapshots.Add("a", 1)
		_, _ = snapshots.Get("a")
		_, _ = webhooks.Get("a")

		// Both cache flavors satisfy StatsProvider and are told apart by the
		// cache label alone.
		assert.NoError(t, testutil.GatherAndCompare(metrics.Registry(), strings.NewReader(`
# HELP yorkie_cache_hits_total The total count of lookups that found an entry in the cache.
# TYPE yorkie_cache_hits_total counter
yorkie_cache_hits_total{cache="snapshots",hostname="test-host"} 1
yorkie_cache_hits_total{cache="auth-webhook",hostname="test-host"} 0
# HELP yorkie_cache_misses_total The total count of lookups that found no entry in the cache.
# TYPE yorkie_cache_misses_total counter
yorkie_cache_misses_total{cache="snapshots",hostname="test-host"} 0
yorkie_cache_misses_total{cache="auth-webhook",hostname="test-host"} 1
`), "yorkie_cache_hits_total", "yorkie_cache_misses_total"))
	})

	t.Run("keep reporting the caches given at registration test", func(t *testing.T) {
		metrics, err := prometheus.NewMetrics()
		assert.NoError(t, err)

		snapshots, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)
		webhooks, err := cache.NewLRUWithExpires[string, int](128, time.Minute, "auth-webhook")
		assert.NoError(t, err)

		// Passing a slice hands the collector the caller's backing array, which
		// the caller is still free to write to.
		registered := []cache.StatsProvider{snapshots}
		assert.NoError(t, metrics.RegisterCaches("test-host", registered...))
		registered[0] = webhooks

		_, _ = snapshots.Get("a")

		assert.NoError(t, testutil.GatherAndCompare(metrics.Registry(), strings.NewReader(`
# HELP yorkie_cache_misses_total The total count of lookups that found no entry in the cache.
# TYPE yorkie_cache_misses_total counter
yorkie_cache_misses_total{cache="snapshots",hostname="test-host"} 1
`), "yorkie_cache_misses_total"))
	})

	t.Run("reject caches sharing a name test", func(t *testing.T) {
		metrics, err := prometheus.NewMetrics()
		assert.NoError(t, err)

		first, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)
		second, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)

		// Two caches under one name would collide on the only label that tells
		// them apart, and a Gather error fails the whole /metrics response.
		assert.Error(t, metrics.RegisterCaches("test-host", first, second))
		assert.NoError(t, testutil.GatherAndCompare(
			metrics.Registry(), strings.NewReader(""), cacheMetricNames...,
		))
	})

	t.Run("reject a second registration test", func(t *testing.T) {
		metrics, err := prometheus.NewMetrics()
		assert.NoError(t, err)

		lru, err := cache.NewLRU[string, int](128, "snapshots")
		assert.NoError(t, err)

		assert.NoError(t, metrics.RegisterCaches("test-host", lru))
		assert.Error(t, metrics.RegisterCaches("test-host", lru))
	})
}
