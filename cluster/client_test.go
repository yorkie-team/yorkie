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
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestOptions(t *testing.T) {
	t.Run("WithRPCTimeout sets rpc timeout", func(t *testing.T) {
		var opts Options
		WithRPCTimeout(5 * time.Second)(&opts)
		assert.Equal(t, 5*time.Second, opts.RPCTimeout)
	})

	t.Run("WithClientTimeout sets client timeout", func(t *testing.T) {
		var opts Options
		WithClientTimeout(15 * time.Second)(&opts)
		assert.Equal(t, 15*time.Second, opts.ClientTimeout)
	})

	t.Run("WithSecure sets secure flag", func(t *testing.T) {
		var opts Options
		WithSecure(true)(&opts)
		assert.True(t, opts.IsSecure)
	})
}

func TestNew(t *testing.T) {
	t.Run("default timeouts when no options provided", func(t *testing.T) {
		client, err := New()
		require.NoError(t, err)
		defer client.Close()

		assert.Equal(t, defaultRPCTimeout, client.rpcTimeout)
		assert.Equal(t, defaultClientTimeout, client.conn.Timeout)
	})

	t.Run("custom timeouts override defaults", func(t *testing.T) {
		client, err := New(
			WithRPCTimeout(5*time.Second),
			WithClientTimeout(15*time.Second),
		)
		require.NoError(t, err)
		defer client.Close()

		assert.Equal(t, 5*time.Second, client.rpcTimeout)
		assert.Equal(t, 15*time.Second, client.conn.Timeout)
	})

	t.Run("zero timeout uses defaults", func(t *testing.T) {
		client, err := New(
			WithRPCTimeout(0),
			WithClientTimeout(0),
		)
		require.NoError(t, err)
		defer client.Close()

		assert.Equal(t, defaultRPCTimeout, client.rpcTimeout)
		assert.Equal(t, defaultClientTimeout, client.conn.Timeout)
	})
}

// newHangingListener creates a TCP listener that accepts connections
// but never sends a response, simulating an unresponsive cluster peer.
func newHangingListener(t *testing.T) net.Listener {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				<-time.After(10 * time.Second)
				c.Close()
			}(conn)
		}
	}()

	return ln
}

func TestRPCTimeout(t *testing.T) {
	t.Run("call is bounded by rpc timeout", func(t *testing.T) {
		ln := newHangingListener(t)
		defer ln.Close()

		rpcTimeout := 200 * time.Millisecond
		client, err := New(
			WithRPCTimeout(rpcTimeout),
			WithClientTimeout(5*time.Second),
		)
		require.NoError(t, err)
		defer client.Close()

		err = client.Dial("http://" + ln.Addr().String())
		require.NoError(t, err)

		start := time.Now()
		err = client.InvalidateCache(context.Background(), types.CacheTypeProject, "key")
		elapsed := time.Since(start)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "deadline exceeded")
		// The elapsed time should be close to rpcTimeout, with some margin.
		assert.GreaterOrEqual(t, elapsed, rpcTimeout)
		assert.Less(t, elapsed, rpcTimeout+500*time.Millisecond)
	})

	t.Run("shorter rpc timeout finishes faster", func(t *testing.T) {
		ln := newHangingListener(t)
		defer ln.Close()

		shortTimeout := 100 * time.Millisecond
		longTimeout := 400 * time.Millisecond

		// Measure with short timeout.
		shortClient, err := New(
			WithRPCTimeout(shortTimeout),
			WithClientTimeout(5*time.Second),
		)
		require.NoError(t, err)
		defer shortClient.Close()
		err = shortClient.Dial("http://" + ln.Addr().String())
		require.NoError(t, err)

		start := time.Now()
		err = shortClient.InvalidateCache(context.Background(), types.CacheTypeProject, "key")
		shortElapsed := time.Since(start)
		assert.Error(t, err)

		// Measure with long timeout.
		longClient, err := New(
			WithRPCTimeout(longTimeout),
			WithClientTimeout(5*time.Second),
		)
		require.NoError(t, err)
		defer longClient.Close()
		err = longClient.Dial("http://" + ln.Addr().String())
		require.NoError(t, err)

		start = time.Now()
		err = longClient.InvalidateCache(context.Background(), types.CacheTypeProject, "key")
		longElapsed := time.Since(start)
		assert.Error(t, err)

		// The long-timeout call should take noticeably longer.
		assert.Greater(t, longElapsed, shortElapsed)
	})

	t.Run("client timeout acts as safety net", func(t *testing.T) {
		ln := newHangingListener(t)
		defer ln.Close()

		clientTimeout := 200 * time.Millisecond
		client, err := New(
			WithRPCTimeout(5*time.Second),
			WithClientTimeout(clientTimeout),
		)
		require.NoError(t, err)
		defer client.Close()

		err = client.Dial("http://" + ln.Addr().String())
		require.NoError(t, err)

		start := time.Now()
		err = client.InvalidateCache(context.Background(), types.CacheTypeProject, "key")
		elapsed := time.Since(start)

		assert.Error(t, err)
		// Even though rpcTimeout is 5s, the client timeout (200ms) should
		// cut off the request earlier.
		assert.Less(t, elapsed, 2*time.Second)
	})

	t.Run("caller context timeout is respected", func(t *testing.T) {
		ln := newHangingListener(t)
		defer ln.Close()

		client, err := New(
			WithRPCTimeout(5*time.Second),
			WithClientTimeout(5*time.Second),
		)
		require.NoError(t, err)
		defer client.Close()

		err = client.Dial("http://" + ln.Addr().String())
		require.NoError(t, err)

		// The caller's context deadline (150ms) is shorter than both
		// rpcTimeout and clientTimeout, so it should take precedence.
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		start := time.Now()
		err = client.InvalidateCache(ctx, types.CacheTypeProject, "key")
		elapsed := time.Since(start)

		assert.Error(t, err)
		assert.Less(t, elapsed, 2*time.Second)
	})
}
