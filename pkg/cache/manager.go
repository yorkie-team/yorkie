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

package cache

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/server/logging"
)

// Manager manages multiple caches and provides periodic logging of statistics.
type Manager struct {
	caches   []StatsProvider
	interval time.Duration
	logger   logging.Logger
	stopCh   chan struct{}
}

// StatsProvider interface for cache objects that provide statistics.
type StatsProvider interface {
	Name() string
	Stats() *Stats
	Len() int
}

// NewManager creates a new cache manager.
func NewManager(interval time.Duration) *Manager {
	return &Manager{
		caches:   make([]StatsProvider, 0),
		interval: interval,
		logger:   logging.New("cache"),
		stopCh:   make(chan struct{}),
	}
}

// RegisterCache registers a cache for monitoring.
func (m *Manager) RegisterCache(cache StatsProvider) {
	m.caches = append(m.caches, cache)
}

// StartPeriodicLogging starts periodic logging of cache statistics.
func (m *Manager) StartPeriodicLogging(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.logCacheStats()
		}
	}
}

// LogCacheStats logs current cache statistics once.
func (m *Manager) LogCacheStats() {
	m.logCacheStats()
}

// logCacheStats logs statistics for all registered caches.
func (m *Manager) logCacheStats() {
	if len(m.caches) == 0 {
		return
	}

	m.logger.Infof("=== Cache Statistics ===")
	for _, cache := range m.caches {
		stats := cache.Stats()
		m.logger.Infof(
			"Cache[%s]: Hits=%d, Misses=%d, Total=%d, HitRate=%.2f%%",
			cache.Name(),
			stats.Hits(),
			stats.Misses(),
			stats.Total(),
			stats.HitRate(),
		)
	}
	m.logger.Infof("========================")
}

// Stop stops the periodic logging.
func (m *Manager) Stop() {
	close(m.stopCh)
}
