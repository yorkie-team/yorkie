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
		t.Skip("TODO(raararaara): Remove this after resolving short renewalInterval issue")
		ctx := context.Background()

		renewalInterval := 5 * gotime.Second

		conf := helper.TestConfig()
		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		hostname1 := svr.Hostname()

		conf2 := helper.TestConfig()
		conf2.Backend.Hostname = "node-2"
		svr2, err := server.New(conf2)
		assert.NoError(t, err)
		assert.NoError(t, svr2.Start())
		hostname2 := svr.Hostname()

		gotime.Sleep(2 * renewalInterval)

		res, err := svr2.FindActiveClusterNodes(ctx, renewalInterval)
		assert.NoError(t, err)
		assert.Equal(t, 2, len(res))
		assert.Equal(t, true, res[0].IsLeader)

		if svr2.Hostname() == res[0].RPCAddr {
			assert.NoError(t, svr2.Shutdown(true))
			defer assert.NoError(t, svr2.Shutdown(true))

			gotime.Sleep(1 * renewalInterval)

			res, err = svr.FindActiveClusterNodes(ctx, renewalInterval)
			assert.NoError(t, err)
			assert.Equal(t, 2, len(res))
			assert.Equal(t, true, res[0].IsLeader)
			assert.Equal(t, hostname2, res[0].RPCAddr)
		} else {
			assert.NoError(t, svr.Shutdown(true))
			defer assert.NoError(t, svr.Shutdown(true))

			gotime.Sleep(1 * renewalInterval)

			res, err = svr2.FindActiveClusterNodes(ctx, renewalInterval)
			assert.NoError(t, err)
			assert.Equal(t, 2, len(res))
			assert.Equal(t, true, res[0].IsLeader)
			assert.Equal(t, hostname1, res[0].RPCAddr)
		}
	})
}
