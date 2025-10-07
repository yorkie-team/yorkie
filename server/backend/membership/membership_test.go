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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/logging"
)

func newDatabase() database.Database {
	db, err := memory.New()
	if err != nil {
		panic(fmt.Sprintf("failed to create in-memory database: %v", err))
	}
	return db
}

func membershipConfig() *Config {
	return &Config{
		LeaseDuration:   "15s",
		RenewalInterval: "5s",
	}
}

func TestMembershipManager(t *testing.T) {
	ctx := context.Background()
	logging.DefaultLogger()

	t.Run("Manager should acquire leadership", func(t *testing.T) {
		db := newDatabase()
		conf := membershipConfig()
		conf.RenewalInterval = "100ms"

		manager := New(db, "node-1", conf)

		err := manager.Start(ctx)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, manager.Stop())
		}()

		assert.Eventually(t, func() bool {
			return manager.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

		lease := manager.CurrentLease()
		require.NotNil(t, lease)
		assert.Equal(t, "node-1", lease.RPCAddr)
	})

	t.Run("Manager should get proper leader", func(t *testing.T) {
		db := newDatabase()

		conf := membershipConfig()
		conf.RenewalInterval = "100ms"

		manager := New(db, "node-1", conf)

		err := manager.Start(ctx)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, manager.Stop())
		}()

		assert.Eventually(t, func() bool {
			infos, err := manager.ClusterNodes(ctx)
			require.NoError(t, err)
			return len(infos) == 1 && infos[0].IsLeader
		}, 1*time.Second, 50*time.Millisecond)
	})

	t.Run("Managers should compete for leadership", func(t *testing.T) {
		db := newDatabase()
		conf := membershipConfig()
		conf.RenewalInterval = "50ms"

		manager1 := New(db, "node-1", conf)
		manager2 := New(db, "node-2", conf)

		err := manager1.Start(ctx)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, manager1.Stop())
		}()

		err = manager2.Start(ctx)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, manager2.Stop())
		}()

		assert.Eventually(t, func() bool {
			return manager1.IsLeader() || manager2.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

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
		conf := membershipConfig()
		conf.LeaseDuration = "200ms"
		conf.RenewalInterval = "50ms"

		manager1 := New(db, "node-1", conf)

		err := manager1.Start(ctx)
		require.NoError(t, err)

		assert.Eventually(t, func() bool {
			return manager1.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)

		assert.NoError(t, manager1.Stop())
		assert.Eventually(t, func() bool {
			infos, err := manager1.ClusterNodes(ctx)
			require.NoError(t, err)
			return len(infos) == 0
		}, 1*time.Second, 50*time.Millisecond)

		manager2 := New(db, "node-2", conf)
		err = manager2.Start(ctx)
		require.NoError(t, err)
		defer func() {
			assert.NoError(t, manager2.Stop())
		}()

		assert.Eventually(t, func() bool {
			return manager2.IsLeader()
		}, 1*time.Second, 50*time.Millisecond)
	})
}

func TestLeadershipConcurrency(t *testing.T) {
	ctx := context.Background()
	db := newDatabase()
	logging.DefaultLogger()

	const numGoroutines = 10
	const leaseDuration = 100 * time.Millisecond

	var wg sync.WaitGroup
	acquiredCount := make([]int, numGoroutines)

	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			rpcAddr := fmt.Sprintf("test-addr-%d", id)

			for range 50 {
				info, err := db.TryLeadership(ctx, rpcAddr, "", leaseDuration)
				if err == nil && info != nil {
					acquiredCount[id]++
					time.Sleep(10 * time.Millisecond)
				}
				time.Sleep(5 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	totalAcquisitions := 0
	for i, count := range acquiredCount {
		t.Logf("Node %d acquired leadership %d times", i, count)
		totalAcquisitions += count
	}

	assert.Greater(t, totalAcquisitions, 0, "At least some leadership acquisitions should have occurred")
}
