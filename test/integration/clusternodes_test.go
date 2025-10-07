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
	"sync"
	"sync/atomic"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
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
	newTestConfig := func(addr string) *server.Config {
		conf := helper.TestConfig()
		conf.Backend.RPCAddr = addr
		conf.Mongo = &mongo.Config{
			ConnectionTimeout:  helper.MongoConnectionTimeout,
			ConnectionURI:      helper.MongoConnectionURI,
			YorkieDatabase:     helper.TestDBName() + "-integration",
			PingTimeout:        helper.MongoPingTimeout,
			CacheStatsInterval: helper.MongoCacheStatsInterval,
			ClientCacheSize:    helper.MongoClientCacheSize,
			DocCacheSize:       helper.MongoDocCacheSize,
			ChangeCacheSize:    helper.MongoChangeCacheSize,
			VectorCacheSize:    helper.MongoVectorCacheSize,
		}
		conf.Membership.LeaseDuration = "300ms"
		conf.Membership.RenewalInterval = "100ms"

		return conf
	}

	startServer := func(addr string) (*server.Yorkie, string) {
		conf := newTestConfig(addr)

		svr, err := server.New(conf)
		require.NoError(t, err)
		require.NoError(t, svr.Start())

		return svr, conf.Backend.RPCAddr
	}

	clearClusterNodes := func(ctx context.Context) {
		dummy, _ := startServer("dummy")
		require.NoError(t, dummy.Backend().ClearClusterNodes(ctx))
		require.NoError(t, dummy.Shutdown(true))
	}

	t.Run("Identical active cluster nodes across servers test", func(t *testing.T) {
		ctx := context.Background()

		clearClusterNodes(ctx)

		numServers := 2

		svrs := make([]*server.Yorkie, numServers)
		for i := range numServers {
			svr, err := server.New(newTestConfig(fmt.Sprintf("test-addr-%d", i)))
			require.NoError(t, err)
			require.NoError(t, svr.Start())
			svrs[i] = svr
		}

		defer func() {
			for _, s := range svrs {
				assert.NoError(t, s.Shutdown(true))
			}
		}()

		toMap := func(infos []*database.ClusterNodeInfo) map[string]struct{} {
			m := make(map[string]struct{}, len(infos))
			for _, in := range infos {
				m[in.RPCAddr] = struct{}{}
			}
			return m
		}
		equal := func(a, b map[string]struct{}) bool {
			if len(a) != len(b) {
				return false
			}
			for k := range a {
				if _, ok := b[k]; !ok {
					return false
				}
			}
			return true
		}

		assert.Eventually(t, func() bool {
			var refMap map[string]struct{}

			for _, svr := range svrs {
				infos, err := svr.Backend().ClusterNodes(ctx)
				require.NoError(t, err)

				if len(infos) != numServers {
					return false
				}
				mp := toMap(infos)

				if refMap == nil {
					refMap = mp
				} else if !equal(refMap, mp) {
					return false
				}
			}
			return true
		}, 1*gotime.Second, 50*gotime.Millisecond)
	})

	t.Run("Leadership reacquisition after server restart test", func(t *testing.T) {
		ctx := context.Background()

		clearClusterNodes(ctx)

		svr, err := server.New(newTestConfig("test-addr-1"))
		assert.NoError(t, err)

		assert.NoError(t, svr.Start())

		assert.Eventually(t, func() bool {
			svrs, err := svr.Backend().ClusterNodes(ctx)
			require.NoError(t, err)
			return 1 == len(svrs) && svrs[0].IsLeader
		}, 1*gotime.Second, 50*gotime.Millisecond)

		assert.NoError(t, svr.Shutdown(true))

		svr2, err := server.New(newTestConfig("test-addr-1"))
		assert.NoError(t, err)

		assert.NoError(t, svr2.Start())
		defer func() {
			assert.NoError(t, svr2.Shutdown(true))
		}()

		// Since there is only one node, that node must be the leader.
		assert.Eventually(t, func() bool {
			infos, err := svr2.Backend().ClusterNodes(ctx)
			require.NoError(t, err)

			return len(infos) > 0 && infos[0].IsLeader && "test-addr-1" == infos[0].RPCAddr
		}, 1*gotime.Second, 50*gotime.Millisecond)
	})

	t.Run("Leadership revocation and reacquisition after temporary DB disconnection test", func(t *testing.T) {
		ctx := context.Background()

		clearClusterNodes(ctx)

		svr, err := server.New(newTestConfig("test-addr-0"))
		assert.NoError(t, err)
		be := svr.Backend()
		mockDB := NewMockDB(be.DB)
		be.SetMembershipDB(mockDB)

		assert.NoError(t, svr.Start())
		defer func() {
			assert.NoError(t, svr.Shutdown(true))
		}()

		var leader *database.ClusterNodeInfo
		assert.Eventually(t, func() bool {
			infos, err := svr.Backend().ClusterNodes(ctx)
			require.NoError(t, err)

			if len(infos) > 0 && infos[0].IsLeader {
				leader = infos[0]
				return true
			}

			return false
		}, 1*gotime.Second, 50*gotime.Millisecond)
		prvToken := leader.LeaseToken

		mockDB.SetDisconnected(true)

		assert.Eventually(t, func() bool {
			infos, err := svr.Backend().ClusterNodes(ctx)
			require.NoError(t, err)

			return len(infos) == 0
		}, 1*gotime.Second, 50*gotime.Millisecond)

		mockDB.SetDisconnected(false)

		assert.Eventually(t, func() bool {
			infos, err := svr.Backend().ClusterNodes(ctx)
			require.NoError(t, err)

			if len(infos) > 0 && infos[0].IsLeader {
				leader = infos[0]
				return true
			}

			return false
		}, 1*gotime.Second, 50*gotime.Millisecond)
		currToken := leader.LeaseToken

		assert.NotEqual(t, prvToken, currToken)
	})

	t.Run("Concurrent connection leadership election test", func(t *testing.T) {
		ctx := context.Background()

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
				if svr.Backend().IsLeader() {
					freq++
				}
			}

			return 1 == freq
		}, 1*gotime.Second, 50*gotime.Millisecond)
	})

	t.Run("Leader graceful shutdown and succession test", func(t *testing.T) {
		ctx := context.Background()

		clearClusterNodes(ctx)

		svr1, addr := startServer("test-addr-1")
		svr2, _ := startServer("test-addr-2")

		assert.Eventually(t, func() bool {
			infos, err := svr2.Backend().ClusterNodes(ctx)
			require.NoError(t, err)
			return 2 == len(infos) && infos[0].IsLeader
		}, 1*gotime.Second, 50*gotime.Millisecond)

		infos, err := svr2.Backend().ClusterNodes(ctx)
		assert.NoError(t, err)
		leader := infos[0].RPCAddr
		var leaderSvr, followerSvr *server.Yorkie

		if leader == addr {
			leaderSvr, followerSvr = svr1, svr2
		} else {
			leaderSvr, followerSvr = svr2, svr1
		}

		require.NoError(t, leaderSvr.Shutdown(true))

		assert.Eventually(t, func() bool {
			return followerSvr.Backend().IsLeader()
		}, 1*gotime.Second, 50*gotime.Millisecond)

		assert.NoError(t, followerSvr.Shutdown(true))
	})
}
