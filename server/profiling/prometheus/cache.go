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

package prometheus

import (
	"slices"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/yorkie-team/yorkie/pkg/cache"
)

// cacheCollector exposes the statistics that the caches already accumulate.
// The values are read when Prometheus scrapes, so the caches keep counting
// exactly once, in their own Get, and the collector adds nothing to the hot
// path.
//
// A hit means the key was present, which is not always the same as the caller
// having avoided work: BuildInternalDocForServerSeq discards a cached document
// that is ahead of the requested server sequence and reads the snapshot from
// the database anyway. The hit rate is therefore an upper bound on how often
// the snapshot path stayed cheap.
type cacheCollector struct {
	hostname string
	caches   []cache.StatsProvider

	hitsDesc   *prometheus.Desc
	missesDesc *prometheus.Desc
}

// newCacheCollector creates a collector reporting the given caches. Caches are
// node-level resources, so the only dimensions are the cache name and the host.
//
// The slice is cloned because Collect walks it on scrape goroutines for the
// life of the process, while the caller's slice remains theirs to append to.
func newCacheCollector(hostname string, caches []cache.StatsProvider) *cacheCollector {
	labels := []string{cacheLabel, hostnameLabel}

	return &cacheCollector{
		hostname: hostname,
		caches:   slices.Clone(caches),
		hitsDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "cache", "hits_total"),
			"The total count of lookups that found an entry in the cache.",
			labels, nil,
		),
		missesDesc: prometheus.NewDesc(
			prometheus.BuildFQName(namespace, "cache", "misses_total"),
			"The total count of lookups that found no entry in the cache.",
			labels, nil,
		),
	}
}

// Describe implements prometheus.Collector.
func (c *cacheCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.hitsDesc
	ch <- c.missesDesc
}

// Collect implements prometheus.Collector.
func (c *cacheCollector) Collect(ch chan<- prometheus.Metric) {
	for _, target := range c.caches {
		stats := target.Stats()
		name := target.Name()

		ch <- prometheus.MustNewConstMetric(
			c.hitsDesc, prometheus.CounterValue, float64(stats.Hits()), name, c.hostname,
		)
		ch <- prometheus.MustNewConstMetric(
			c.missesDesc, prometheus.CounterValue, float64(stats.Misses()), name, c.hostname,
		)
	}
}
