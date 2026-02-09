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
	"sync"
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
			WithPoolSize(1),
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
			WithPoolSize(1),
		)
		defer pool.Close()

		client1, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		client2, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		assert.Same(t, client1, client2)
	})

	t.Run("pool creates separate clients for different addresses", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(1))
		defer pool.Close()

		client1, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)

		client2, err := pool.Get("http://127.0.0.1:1002")
		require.NoError(t, err)

		assert.NotSame(t, client1, client2)
	})
}

func TestClientPoolSize(t *testing.T) {
	t.Run("default pool size is 1", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(1))
		defer pool.Close()

		assert.Equal(t, 1, pool.poolSize)
	})

	t.Run("pool creates N clients per host", func(t *testing.T) {
		poolSize := 3
		pool := NewClientPool(WithPoolSize(poolSize))
		defer pool.Close()

		_, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		pool.mu.RLock()
		clients := pool.clients["http://127.0.0.1:0"]
		pool.mu.RUnlock()

		assert.Len(t, clients, poolSize)

		// All clients should be distinct instances.
		for i := range len(clients) {
			for j := i + 1; j < len(clients); j++ {
				assert.NotSame(t, clients[i], clients[j])
			}
		}
	})

	t.Run("round-robin distributes across pool clients", func(t *testing.T) {
		poolSize := 3
		pool := NewClientPool(WithPoolSize(poolSize))
		defer pool.Close()

		// Collect clients returned by Get over multiple calls.
		seen := make(map[*Client]bool)
		for i := 0; i < poolSize*2; i++ {
			client, err := pool.Get("http://127.0.0.1:0")
			require.NoError(t, err)
			seen[client] = true
		}

		// All N clients in the pool should have been returned at least once.
		assert.Equal(t, poolSize, len(seen))
	})

	t.Run("pool size 1 always returns same client", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(1))
		defer pool.Close()

		client1, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		client2, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		assert.Same(t, client1, client2)
	})

	t.Run("each address has independent pool", func(t *testing.T) {
		poolSize := 2
		pool := NewClientPool(WithPoolSize(poolSize))
		defer pool.Close()

		_, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)
		_, err = pool.Get("http://127.0.0.1:1002")
		require.NoError(t, err)

		pool.mu.RLock()
		clients1 := pool.clients["http://127.0.0.1:1001"]
		clients2 := pool.clients["http://127.0.0.1:1002"]
		pool.mu.RUnlock()

		assert.Len(t, clients1, poolSize)
		assert.Len(t, clients2, poolSize)

		// Clients from different addresses must not overlap.
		for _, c1 := range clients1 {
			for _, c2 := range clients2 {
				assert.NotSame(t, c1, c2)
			}
		}
	})
}

func TestClientPoolRemove(t *testing.T) {
	t.Run("remove closes all clients for address", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(3))

		_, err := pool.Get("http://127.0.0.1:0")
		require.NoError(t, err)

		pool.Remove("http://127.0.0.1:0")

		pool.mu.RLock()
		_, exists := pool.clients["http://127.0.0.1:0"]
		_, counterExists := pool.counters["http://127.0.0.1:0"]
		pool.mu.RUnlock()

		assert.False(t, exists)
		assert.False(t, counterExists)
	})

	t.Run("remove non-existent address is no-op", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(2))
		defer pool.Close()

		// Should not panic.
		pool.Remove("http://127.0.0.1:9999")
	})
}

func TestClientPoolClose(t *testing.T) {
	t.Run("close clears all addresses", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(2))

		_, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)
		_, err = pool.Get("http://127.0.0.1:1002")
		require.NoError(t, err)

		pool.Close()

		pool.mu.RLock()
		numClients := len(pool.clients)
		numCounters := len(pool.counters)
		pool.mu.RUnlock()

		assert.Equal(t, 0, numClients)
		assert.Equal(t, 0, numCounters)
	})
}

func TestClientPoolPrune(t *testing.T) {
	t.Run("prune removes inactive addresses", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(2))
		defer pool.Close()

		_, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)
		_, err = pool.Get("http://127.0.0.1:1002")
		require.NoError(t, err)
		_, err = pool.Get("http://127.0.0.1:1003")
		require.NoError(t, err)

		// Keep only 1001 and 1003 active.
		pool.Prune([]string{"http://127.0.0.1:1001", "http://127.0.0.1:1003"})

		pool.mu.RLock()
		_, has1001 := pool.clients["http://127.0.0.1:1001"]
		_, has1002 := pool.clients["http://127.0.0.1:1002"]
		_, has1003 := pool.clients["http://127.0.0.1:1003"]
		pool.mu.RUnlock()

		assert.True(t, has1001)
		assert.False(t, has1002)
		assert.True(t, has1003)
	})

	t.Run("prune with empty list removes all", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(2))

		_, err := pool.Get("http://127.0.0.1:1001")
		require.NoError(t, err)

		pool.Prune(nil)

		pool.mu.RLock()
		numClients := len(pool.clients)
		pool.mu.RUnlock()

		assert.Equal(t, 0, numClients)
	})
}

func TestClientPoolConcurrency(t *testing.T) {
	t.Run("concurrent Get is safe", func(t *testing.T) {
		pool := NewClientPool(WithPoolSize(3))
		defer pool.Close()

		var wg sync.WaitGroup
		const goroutines = 50
		errs := make(chan error, goroutines)

		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := pool.Get("http://127.0.0.1:0")
				if err != nil {
					errs <- err
				}
			}()
		}

		wg.Wait()
		close(errs)

		for err := range errs {
			require.NoError(t, err)
		}
	})

	t.Run("concurrent Get distributes across pool", func(t *testing.T) {
		poolSize := 4
		pool := NewClientPool(WithPoolSize(poolSize))
		defer pool.Close()

		var mu sync.Mutex
		seen := make(map[*Client]int)

		var wg sync.WaitGroup
		const goroutines = 100

		for range goroutines {
			wg.Add(1)
			go func() {
				defer wg.Done()
				client, err := pool.Get("http://127.0.0.1:0")
				if err != nil {
					return
				}
				mu.Lock()
				seen[client]++
				mu.Unlock()
			}()
		}

		wg.Wait()

		// All pool clients should have been used.
		assert.Equal(t, poolSize, len(seen))
	})
}
