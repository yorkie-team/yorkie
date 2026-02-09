/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package cluster

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientPool(t *testing.T) {
	t.Run("pool passes options to created clients", func(t *testing.T) {
		pool := NewClientPool(
			WithRPCTimeout(3*time.Second),
			WithClientTimeout(7*time.Second),
		)
		defer pool.Close()

		client, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		assert.Equal(t, 3*time.Second, client.rpcTimeout)
		assert.Equal(t, 7*time.Second, client.conn.Timeout)
	})

	t.Run("pool reuses existing clients", func(t *testing.T) {
		pool := NewClientPool(
			WithRPCTimeout(3*time.Second),
			WithClientTimeout(7*time.Second),
		)
		defer pool.Close()

		client1, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		client2, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		assert.Same(t, client1, client2)
	})

	t.Run("pool creates separate clients for different addresses", func(t *testing.T) {
		pool := NewClientPool()
		defer pool.Close()

		client1, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)

		client2, err := pool.Get("http://127.0.0.1:1002")
		require.NoError(t, err)

		assert.NotSame(t, client1, client2)
	})

	t.Run("pool uses default timeouts when no options provided", func(t *testing.T) {
		pool := NewClientPool()
		defer pool.Close()

		client, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		assert.Equal(t, defaultRPCTimeout, client.rpcTimeout)
		assert.Equal(t, defaultClientTimeout, client.conn.Timeout)
	})
}
