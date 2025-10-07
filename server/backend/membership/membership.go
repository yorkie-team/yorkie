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

package membership

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Manager manages leader election and lease renewal.
type Manager struct {
	rpcAddr         string
	leaseDuration   time.Duration
	renewalInterval time.Duration

	database database.Database
	logger   logging.Logger
	stopCh   chan struct{}
	wg       sync.WaitGroup
	mutex    sync.RWMutex

	isLeader atomic.Bool
	lease    atomic.Pointer[database.ClusterNodeInfo]
}

// New creates a new membership manager.
func New(db database.Database, rpcAddr string, conf *Config) *Manager {
	if rpcAddr == "" {
		panic("rpcAddr must not be empty")
	}

	leaseDuration, err := conf.ParseLeaseDuration()
	if err != nil {
		panic(fmt.Errorf("failed to parse lease duration: %w", err))
	}

	renewalInterval, err := conf.ParseRenewalInterval()
	if err != nil {
		panic(fmt.Errorf("failed to parse renewal interval: %w", err))
	}

	return &Manager{
		rpcAddr:         rpcAddr,
		leaseDuration:   leaseDuration,
		renewalInterval: renewalInterval,

		database: db,
		stopCh:   make(chan struct{}),
	}
}

// Start starts the membership manager.
func (mm *Manager) Start(ctx context.Context) error {
	mm.mutex.Lock()
	defer mm.mutex.Unlock()

	mm.wg.Add(1)
	go mm.membershipLoop(ctx)

	return nil
}

// Stop stops the membership manager.
func (mm *Manager) Stop() error {
	mm.mutex.Lock()
	defer mm.mutex.Unlock()

	close(mm.stopCh)
	mm.wg.Wait()

	return mm.database.RemoveClusterNode(context.Background(), mm.rpcAddr)
}

// IsLeader returns true if the current node is the leader.
func (mm *Manager) IsLeader() bool {
	return mm.isLeader.Load()
}

// ClusterNodes returns the active cluster nodes.
func (mm *Manager) ClusterNodes(ctx context.Context) ([]*database.ClusterNodeInfo, error) {
	// NOTE: Use renewalInterval * 2 to account for possible delays in lease renewal.
	return mm.database.FindClusterNodes(ctx, mm.renewalInterval*2)
}

// CurrentLease returns the current lease information if this node is the leader.
func (mm *Manager) CurrentLease() *database.ClusterNodeInfo {
	return mm.lease.Load()
}

// SetDB sets the given database to this manager(for testing purposes).
func (mm *Manager) SetDB(db database.Database) {
	mm.database = db
}

// membershipLoop runs the membership management loop.
func (mm *Manager) membershipLoop(ctx context.Context) {
	defer mm.wg.Done()

	for {
		mm.handleMembershipCycle(ctx)

		select {
		case <-time.After(mm.renewalInterval):

		case <-mm.stopCh:
			return

		case <-ctx.Done():
			return
		}
	}
}

// handleMembershipCycle handles one cycle of membership management.
func (mm *Manager) handleMembershipCycle(ctx context.Context) {
	if mm.isLeader.Load() {
		if err := mm.renewLeadership(ctx); err != nil {
			logging.LogError(ctx, err)
			mm.becomeFollower()
		}
		return
	}

	if err := mm.tryAcquireLeadership(ctx); err != nil {
		logging.From(ctx).Debug("failed to acquire leadership", "error", err)
	}
}

// tryAcquireLeadership attempts to acquire leadership.
func (mm *Manager) tryAcquireLeadership(ctx context.Context) error {
	lease, err := mm.database.TryLeadership(ctx, mm.rpcAddr, "", mm.leaseDuration)
	if err != nil {
		return fmt.Errorf("acquire leadership: %w", err)
	}
	if lease == nil {
		return nil
	}

	mm.becomeLeader(lease)
	logging.From(ctx).Infof("leadership acquired, expires_at: %s", lease.ExpiresAt)

	return nil
}

// renewLeadership renews the current leadership.
func (mm *Manager) renewLeadership(ctx context.Context) error {
	lease := mm.lease.Load()
	if lease == nil {
		return fmt.Errorf("no current lease to renew")
	}

	nextLease, err := mm.database.TryLeadership(ctx, mm.rpcAddr, lease.LeaseToken, mm.leaseDuration)
	if err != nil {
		return fmt.Errorf("renew leadership: %w", err)
	}

	mm.lease.Store(nextLease)
	logging.From(ctx).Debug("renewed leadership lease", "expires_at", nextLease.ExpiresAt)

	return nil
}

// becomeLeader transitions to leader state.
func (mm *Manager) becomeLeader(lease *database.ClusterNodeInfo) {
	mm.isLeader.Store(true)
	mm.lease.Store(lease)
}

// becomeFollower transitions to follower state.
func (mm *Manager) becomeFollower() {
	mm.isLeader.Store(false)
	mm.lease.Store(nil)
}
