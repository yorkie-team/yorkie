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

package housekeeping

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// LeadershipManager manages leader election and lease renewal.
type LeadershipManager struct {
	database        database.Database
	hostname        string
	leaseDuration   time.Duration
	renewalInterval time.Duration

	// State management
	isLeader     atomic.Bool
	currentLease atomic.Pointer[database.LeadershipInfo]
	stopCh       chan struct{}
	wg           sync.WaitGroup
	mutex        sync.RWMutex
}

// LeadershipConfig contains configuration for leadership management.
type LeadershipConfig struct {
	LeaseDuration   time.Duration
	RenewalInterval time.Duration
}

// DefaultLeadershipConfig returns the default leadership configuration.
func DefaultLeadershipConfig() *LeadershipConfig {
	return &LeadershipConfig{
		LeaseDuration:   15 * time.Second,
		RenewalInterval: 5 * time.Second,
	}
}

// NewLeadershipManager creates a new leadership manager.
func NewLeadershipManager(db database.Database, hostname string, conf *LeadershipConfig) *LeadershipManager {
	if hostname == "" {
		panic("hostname must not be empty")
	}

	if conf == nil {
		conf = DefaultLeadershipConfig()
	}

	return &LeadershipManager{
		database:        db,
		hostname:        hostname,
		leaseDuration:   conf.LeaseDuration,
		renewalInterval: conf.RenewalInterval,
		stopCh:          make(chan struct{}),
	}
}

// Start starts the leadership manager.
func (lm *LeadershipManager) Start(ctx context.Context) error {
	lm.mutex.Lock()
	defer lm.mutex.Unlock()

	// Start leadership acquisition loop
	lm.wg.Add(1)
	go lm.leadershipLoop(ctx)

	if logger := logging.From(ctx); logger != nil {
		logger.Infof("leadership manager started: %s", lm.hostname)
	}
	return nil
}

// Stop stops the leadership manager.
func (lm *LeadershipManager) Stop() {
	lm.mutex.Lock()
	defer lm.mutex.Unlock()

	close(lm.stopCh)
	lm.wg.Wait()
}

// IsLeader returns true if the current node is the leader.
func (lm *LeadershipManager) IsLeader() bool {
	return lm.isLeader.Load()
}

// Leader returns the current leader information.
func (lm *LeadershipManager) Leader(ctx context.Context) (*database.LeadershipInfo, error) {
	return lm.database.FindLeadership(ctx)
}

// CurrentLease returns the current lease information if this node is the leader.
func (lm *LeadershipManager) CurrentLease() *database.LeadershipInfo {
	return lm.currentLease.Load()
}

// leadershipLoop manages the leadership acquisition and renewal process.
func (lm *LeadershipManager) leadershipLoop(ctx context.Context) {
	defer lm.wg.Done()

	ticker := time.NewTicker(lm.renewalInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			lm.handleLeadershipCycle(ctx)

		case <-lm.stopCh:
			return

		case <-ctx.Done():
			return
		}
	}
}

// handleLeadershipCycle handles one cycle of leadership management.
func (lm *LeadershipManager) handleLeadershipCycle(ctx context.Context) {
	if lm.isLeader.Load() {
		// We are the leader, try to renew the lease
		if err := lm.renewLease(ctx); err != nil {
			if logger := logging.From(ctx); logger != nil {
				logger.Warn("failed to renew leadership lease", "error", err)
			}
			lm.becomeFollower()
		}
	} else {
		// We are not the leader, try to acquire leadership
		if err := lm.tryAcquireLeadership(ctx); err != nil {
			if logger := logging.From(ctx); logger != nil {
				logger.Debug("failed to acquire leadership", "error", err)
			}
		}
	}
}

// tryAcquireLeadership attempts to acquire leadership.
func (lm *LeadershipManager) tryAcquireLeadership(ctx context.Context) error {
	lease, err := lm.database.TryLeadership(ctx, lm.hostname, "", lm.leaseDuration)
	if err != nil {
		return fmt.Errorf("acquire leadership: %w", err)
	}

	if lease.Hostname == lm.hostname {
		lm.becomeLeader(lease)
		if logger := logging.From(ctx); logger != nil {
			logger.Infof("leadership acquired term: %d, expires_at: %s", lease.Term, lease.ExpiresAt)
		}
	}

	return nil
}

// renewLease renews the current leadership lease.
func (lm *LeadershipManager) renewLease(ctx context.Context) error {
	currentLease := lm.currentLease.Load()
	if currentLease == nil {
		return fmt.Errorf("no current lease to renew")
	}

	lease, err := lm.database.TryLeadership(ctx, lm.hostname, currentLease.LeaseToken, lm.leaseDuration)
	if err != nil {
		return fmt.Errorf("renew leadership: %w", err)
	}

	lm.currentLease.Store(lease)
	if logger := logging.From(ctx); logger != nil {
		logger.Debug("renewed leadership lease", "expires_at", lease.ExpiresAt)
	}

	return nil
}

// becomeLeader transitions to leader state.
func (lm *LeadershipManager) becomeLeader(lease *database.LeadershipInfo) {
	lm.isLeader.Store(true)
	lm.currentLease.Store(lease)
}

// becomeFollower transitions to follower state.
func (lm *LeadershipManager) becomeFollower() {
	lm.isLeader.Store(false)
	lm.currentLease.Store(nil)
}
