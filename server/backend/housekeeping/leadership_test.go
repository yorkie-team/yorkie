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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
)

func newDatabase() database.Database {
	// Create a new in-memory database for testing
	db, err := memory.New()
	if err != nil {
		panic(fmt.Sprintf("failed to create in-memory database: %v", err))
	}

	return db
}

func TestLeadershipManager(t *testing.T) {
	ctx := context.Background()

	t.Run("LeadershipManager should acquire leadership", func(t *testing.T) {
		db := newDatabase()
		conf := DefaultLeadershipConfig()
		conf.RenewalInterval = 100 * time.Millisecond

		manager := NewLeadershipManager(db, "node-1", conf)

		err := manager.Start(ctx)
		require.NoError(t, err)
		defer manager.Stop()

		// Wait for leadership acquisition
		assert.Eventually(t, func() bool {
			return manager.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

		// Verify leadership info
		lease := manager.CurrentLease()
		require.NotNil(t, lease)
		assert.Equal(t, "node-1", lease.Hostname)
	})

	t.Run("Multiple managers should compete for leadership", func(t *testing.T) {
		db := newDatabase()
		conf := DefaultLeadershipConfig()
		conf.RenewalInterval = 50 * time.Millisecond

		manager1 := NewLeadershipManager(db, "node-1", conf)
		manager2 := NewLeadershipManager(db, "node-2", conf)

		err := manager1.Start(ctx)
		require.NoError(t, err)
		defer manager1.Stop()

		err = manager2.Start(ctx)
		require.NoError(t, err)
		defer manager2.Stop()

		// Wait for one to become leader
		assert.Eventually(t, func() bool {
			return manager1.IsLeader() || manager2.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

		// Only one should be leader
		leaders := 0
		if manager1.IsLeader() {
			leaders++
		}
		if manager2.IsLeader() {
			leaders++
		}
		assert.Equal(t, 1, leaders)
	})

	t.Run("Leadership should transfer after lease expires", func(t *testing.T) {
		db := newDatabase()
		conf := DefaultLeadershipConfig()
		conf.LeaseDuration = 200 * time.Millisecond
		conf.RenewalInterval = 50 * time.Millisecond

		manager1 := NewLeadershipManager(db, "node-1", conf)

		err := manager1.Start(ctx)
		require.NoError(t, err)

		// Wait for leadership acquisition
		assert.Eventually(t, func() bool {
			return manager1.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

		// Stop manager1 (simulating node failure)
		manager1.Stop()

		// Start manager2
		manager2 := NewLeadershipManager(db, "node-2", conf)
		err = manager2.Start(ctx)
		require.NoError(t, err)
		defer manager2.Stop()

		// Manager2 should eventually become leader
		assert.Eventually(t, func() bool {
			return manager2.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)
	})
}

func TestLeadershipConcurrency(t *testing.T) {
	ctx := context.Background()
	db := newDatabase()

	const numGoroutines = 10
	const leaseDuration = 100 * time.Millisecond

	var wg sync.WaitGroup
	acquiredCount := make([]int, numGoroutines)

	// Start multiple goroutines trying to acquire leadership
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			nodeID := fmt.Sprintf("node-%d", id)

			for range 50 {
				info, err := db.TryLeadership(ctx, nodeID, "", leaseDuration)
				if err == nil && info.Hostname == nodeID {
					acquiredCount[id]++

					// Hold leadership briefly then let it expire
					time.Sleep(10 * time.Millisecond)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// Verify that leadership was acquired by various nodes
	totalAcquisitions := 0
	for i, count := range acquiredCount {
		t.Logf("Node %d acquired leadership %d times", i, count)
		totalAcquisitions += count
	}

	assert.Greater(t, totalAcquisitions, 0, "At least some leadership acquisitions should have occurred")
}
