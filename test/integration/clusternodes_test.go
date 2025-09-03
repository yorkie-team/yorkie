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
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClusterNodes(t *testing.T) {
	t.Run("Should handle leader graceful shutdown test", func(t *testing.T) {
		ctx := context.Background()

		renewalInterval := 100 * gotime.Millisecond

		startServer := func(name string) (*server.Yorkie, string) {
			conf := helper.TestConfig()
			conf.Backend.Hostname = name
			conf.Housekeeping.LeadershipLeaseDuration = "300ms"
			conf.Housekeeping.LeadershipRenewalInterval = "100ms"
			svr, err := server.New(conf)
			assert.NoError(t, err)
			assert.NoError(t, svr.Start())
			return svr, conf.Backend.Hostname
		}

		svr1, hostname1 := startServer("node-1")
		svr2, hostname2 := startServer("node-2")

		gotime.Sleep(3 * renewalInterval)

		res, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(res))
		assert.Eventually(t, func() bool {
			return res[0].IsLeader
		}, renewalInterval, 4*renewalInterval)

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

		assert.NoError(t, leaderSvr.Shutdown(true))

		gotime.Sleep(5 * renewalInterval)

		res, err = followerSvr.FindActiveClusterNodes(ctx, renewalInterval)
		assert.NoError(t, err)
		assert.Equal(t, 1, len(res))
		assert.Eventually(t, func() bool {
			return res[0].IsLeader
		}, renewalInterval, 4*renewalInterval)
		assert.Equal(t, followerName, res[0].RPCAddr)

		assert.NoError(t, followerSvr.Shutdown(true))
	})
}
