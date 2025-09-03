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
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClusterNodes(t *testing.T) {
	getFreePort := func() int {
		l, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			panic(err)
		}
		defer assert.NoError(t, l.Close())
		return l.Addr().(*net.TCPAddr).Port
	}

	startServer := func(name string) (*server.Yorkie, string) {
		conf := helper.TestConfig()
		conf.RPC.Port = getFreePort()
		conf.Profiling.Port = getFreePort()
		conf.Backend.Hostname = name
		conf.Housekeeping.LeadershipLeaseDuration = "300ms"
		conf.Housekeeping.LeadershipRenewalInterval = "100ms"

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

	t.Run("Should handle concurrent connection test", func(t *testing.T) {
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		numGoroutines := 5
		var wg sync.WaitGroup
		svrCh := make(chan *server.Yorkie, numGoroutines)

		for i := range numGoroutines {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				svr, _ := startServer(fmt.Sprintf("node-%d", id))
				svrCh <- svr
			}(i)
		}
		wg.Wait()
		close(svrCh)

		var svrs []*server.Yorkie
		for s := range svrCh {
			svrs = append(svrs, s)
		}
		defer func() {
			for _, s := range svrs {
				_ = s.Shutdown(true)
			}
		}()

		refSvr := svrs[0]

		gotime.Sleep(3 * renewalInterval)

		infos, err := refSvr.FindActiveClusterNodes(ctx, renewalInterval)
		require.NoError(t, err)
		assert.Equal(t, numGoroutines, len(infos))
		assert.True(t, infos[0].IsLeader)

		leaderCount := 0
		for _, info := range infos {
			if info.IsLeader {
				leaderCount++
			}
		}
		assert.Equal(t, 1, leaderCount)
	})

	t.Run("Should handle leader graceful shutdown test", func(t *testing.T) {
		ctx := context.Background()
		renewalInterval := 100 * gotime.Millisecond

		clearClusterNodes(ctx)

		svr1, hostname1 := startServer("node-1")
		svr2, hostname2 := startServer("node-2")

		gotime.Sleep(3 * renewalInterval)

		res, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
		require.NoError(t, err)
		assert.Equal(t, 2, len(res))
		assert.True(t, res[0].IsLeader)

		leader := res[0].RPCAddr
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

		gotime.Sleep(5 * renewalInterval)

		res, err = followerSvr.FindActiveClusterNodes(ctx, renewalInterval)
		require.NoError(t, err)
		assert.Equal(t, 1, len(res))
		assert.True(t, res[0].IsLeader)
		assert.Equal(t, followerName, res[0].RPCAddr)

		assert.NoError(t, followerSvr.Shutdown(true))
	})
}
