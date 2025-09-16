package integration

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

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
)

// MockDB represents a mock database for testing purposes
type MockDB struct {
	database.Database

	isDisconnected atomic.Bool
}

// NewMockDB returns a mock database with a real database
func NewMockDB(database database.Database) *MockDB {
	return &MockDB{
		Database: database,
	}
}

func (m *MockDB) TryLeadership(
	ctx context.Context,
	rpcAddr,
	leaseToken string,
	leaseDuration gotime.Duration,
) (*database.ClusterNodeInfo, error) {
	if m.isDisconnected.Load() {
		return nil, fmt.Errorf("database unavailable: mock disconnection")
	}
	return m.Database.TryLeadership(ctx, rpcAddr, leaseToken, leaseDuration)
}

func (m *MockDB) SetDisconnected(disconnected bool) {
	m.isDisconnected.Store(disconnected)
}

func TestClusterNodes(t *testing.T) {
	getFreePort := func() int {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
		defer assert.NoError(t, l.Close())
		return l.Addr().(*net.TCPAddr).Port
	}

	newTestConfig := func(addr string) *server.Config {
		conf := helper.TestConfig()
		conf.RPC.Port = getFreePort()
		conf.Profiling.Port = getFreePort()
		conf.Backend.RPCAddr = addr
		conf.Housekeeping.LeadershipLeaseDuration = "300ms"
		conf.Housekeeping.LeadershipRenewalInterval = "100ms"

		return conf
	}

	startServer := func(addr string) (*server.Yorkie, string) {
		conf := newTestConfig(addr)

		svr, err := server.New(conf)
		require.NoError(t, err)
		require.NoError(t, svr.Start())

		return svr, conf.Backend.Hostname
	}

	clearClusterNodes := func(ctx context.Context) {
		dummy, _ := startServer("dummy")
		require.NoError(t, dummy.ClearClusterNodes(ctx))
		require.NoError(t, dummy.Shutdown(true))
	}

	t.Run("Active Cluster nodes test", func(t *testing.T) {
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		numGoroutines := 2

		svrs := make([]*server.Yorkie, numGoroutines)
		for i := range numGoroutines {
			svr, err := server.New(newTestConfig(fmt.Sprintf("test-addr-%d", i)))
			require.NoError(t, err)
			svrs[i] = svr
		}

		var wg sync.WaitGroup
		for _, svr := range svrs {
			wg.Add(1)
			go func(svr *server.Yorkie) {
				defer wg.Done()
				require.NoError(t, svr.Start())
			}(svr)
		}
		wg.Wait()
		defer func() {
			for _, s := range svrs {
				assert.NoError(t, s.Shutdown(true))
			}
		}()

		assert.Eventually(t, func() bool {
			infos1, err := svrs[0].FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)
			infos2, err := svrs[1].FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)

			return len(infos1) == numGoroutines &&
				len(infos2) == numGoroutines &&
				infos1[0].RPCAddr == infos2[0].RPCAddr &&
				infos1[1].RPCAddr == infos2[1].RPCAddr
		}, 20*renewalInterval, renewalInterval)
	})

	t.Run("Should handle server restart test", func(t *testing.T) {
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		svr, err := server.New(newTestConfig("test-addr-1"))
		assert.NoError(t, err)

		assert.NoError(t, svr.Start())
		gotime.Sleep(500 * gotime.Millisecond)

		assert.Eventually(t, func() bool {
			svrs, err := svr.FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)
			return 1 == len(svrs) && svrs[0].IsLeader
		}, 10*renewalInterval, renewalInterval)
		prvLeader, err := svr.FindLeadership(ctx)
		assert.NoError(t, err)

		assert.NoError(t, svr.Shutdown(true))
		gotime.Sleep(500 * gotime.Millisecond)

		svr2, err := server.New(newTestConfig("test-addr-2"))
		assert.NoError(t, err)

		assert.NoError(t, svr2.Start())

		assert.Eventually(t, func() bool {
			svrs, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)

			return 1 == len(svrs)
		}, 50*renewalInterval, renewalInterval)
		currLeader, err := svr2.FindLeadership(ctx)
		assert.NoError(t, err)

		assert.NotEqual(t, prvLeader.LeaseToken, currLeader.LeaseToken)

		assert.NoError(t, svr2.Shutdown(true))
	})
	t.Run("Should revoke and reacquire leadership after temporary DB disconnection", func(t *testing.T) {
		ctx := context.Background()

		clearClusterNodes(ctx)

		svr, err := server.New(newTestConfig("test-addr-0"))
		assert.NoError(t, err)
		be := svr.Backend()
		mockDB := NewMockDB(be.DB)
		be.Housekeeping.SetLeadershipDB(mockDB)

		assert.NoError(t, svr.Start())

		assert.Eventually(t, func() bool {
			leader, err := svr.FindLeadership(ctx)
			require.NoError(t, err)

			return leader != nil
		}, 1000*gotime.Second, 100*gotime.Millisecond)

		leader, err := svr.FindLeadership(ctx)
		prvToken := leader.LeaseToken
		assert.NoError(t, err)
		assert.True(t, leader.IsLeader)

		mockDB.SetDisconnected(true)

		assert.Eventually(t, func() bool {
			leader, err := svr.FindLeadership(ctx)
			require.NoError(t, err)

			return leader == nil
		}, 1000*gotime.Second, 100*gotime.Millisecond)

		mockDB.SetDisconnected(false)

		assert.Eventually(t, func() bool {
			leader, err = svr.FindLeadership(ctx)
			require.NoError(t, err)

			return leader != nil
		}, 1000*gotime.Second, 100*gotime.Millisecond)
		currToken := leader.LeaseToken

		assert.NotEqual(t, prvToken, currToken)
	})

	t.Run("Should handle concurrent connection test", func(t *testing.T) {
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		numGoroutines := 5

		svrs := make([]*server.Yorkie, numGoroutines)
		for i := range numGoroutines {
			svr, err := server.New(newTestConfig(fmt.Sprintf("test-addr-%d", i)))
			require.NoError(t, err)
			svrs[i] = svr
		}

		var wg sync.WaitGroup
		for _, svr := range svrs {
			wg.Add(1)
			go func(svr *server.Yorkie) {
				defer wg.Done()
				require.NoError(t, svr.Start())
			}(svr)
		}
		wg.Wait()
		defer func() {
			for _, s := range svrs {
				assert.NoError(t, s.Shutdown(true))
			}
		}()

		assert.Eventually(t, func() bool {
			freq := 0

			for _, svr := range svrs {
				if svr.IsLeader() {
					freq++
				}
			}

			return 1 == freq
		}, 20*renewalInterval, renewalInterval)
	})

	t.Run("Should handle leader graceful shutdown test", func(t *testing.T) {
		t.Skip("TODO(raararaara): Remove after adding TTL index setup for tests.")
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		svr1, hostname1 := startServer("test-addr-1")
		svr2, hostname2 := startServer("test-addr-2")

		assert.Eventually(t, func() bool {
			infos, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)
			return 2 == len(infos) && infos[0].IsLeader
		}, 10*renewalInterval, renewalInterval)

		infos, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
		assert.NoError(t, err)
		leader := infos[0].RPCAddr
		var leaderSvr, followerSvr *server.Yorkie
		var followerName string

		if leader == hostname1 {
			leaderSvr, followerSvr = svr1, svr2
			followerName = hostname2
		} else {
			leaderSvr, followerSvr = svr2, svr1
			followerName = hostname1
		}

		require.NoError(t, leaderSvr.Shutdown(true))

		assert.Eventually(t, func() bool {
			infos, err = followerSvr.FindActiveClusterNodes(ctx, renewalInterval)
			require.NoError(t, err)

			return 1 == len(infos) && infos[0].IsLeader && followerName == infos[0].RPCAddr
		}, 100*renewalInterval, renewalInterval)

		assert.NoError(t, followerSvr.Shutdown(true))
	})
}
