//go:build integration

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

package integration

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestChannelIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("single client channel test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)

		cli := clients[0]

		// Create channel
		channelKey := helper.TestKey(t)
		ch, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Test initial state
		assert.Equal(t, channelKey, ch.Key())
		assert.Equal(t, attachable.TypeChannel, ch.Type())
		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())

		// Attach channel
		err = cli.Attach(ctx, ch)
		require.NoError(t, err)

		// Verify attached state
		assert.Equal(t, attachable.StatusAttached, ch.Status())
		assert.True(t, ch.IsAttached())

		// Detach channel
		require.NoError(t, cli.Detach(ctx, ch))

		// Verify detached state
		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())
	})

	t.Run("multiple clients channel test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		client1, client2, client3 := clients[0], clients[1], clients[2]

		// Create channels for the same room
		channelKey := helper.TestKey(t)
		ch1, err := channel.New(channelKey)
		assert.NoError(t, err)
		ch2, err := channel.New(channelKey)
		assert.NoError(t, err)
		ch3, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Attach first client
		err = client1.Attach(ctx, ch1)
		assert.NoError(t, err)
		require.NoError(t, err)

		// Attach second client
		err = client2.Attach(ctx, ch2)
		require.NoError(t, err)

		// Attach third client
		err = client3.Attach(ctx, ch3)
		require.NoError(t, err)

		// All should be attached
		assert.True(t, ch1.IsAttached())
		assert.True(t, ch2.IsAttached())
		assert.True(t, ch3.IsAttached())

		// Detach clients
		require.NoError(t, client1.Detach(ctx, ch1))
		assert.False(t, ch1.IsAttached())
		require.NoError(t, client2.Detach(ctx, ch2))
		assert.False(t, ch2.IsAttached())
		require.NoError(t, client3.Detach(ctx, ch3))
		assert.False(t, ch3.IsAttached())
	})

	t.Run("channel stress test", func(t *testing.T) {
		clientCount := 5

		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		channelKey := helper.TestKey(t)
		channels := make([]*channel.Channel, clientCount)

		// Create and activate multiple clients
		for i := range clientCount {
			var err error
			channels[i], err = channel.New(channelKey)
			assert.NoError(t, err)
		}

		// Attach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				if err := clients[idx].Attach(ctx, channels[idx]); err != nil {
					t.Errorf("Failed to attach channel %d: %v", idx, err)
				}
			}(i)
		}

		// Wait for all attachments using assert.Eventually
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if !channels[i].IsAttached() {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All clients should be attached")

		// Verify all are attached
		for i := range clientCount {
			assert.True(t, channels[i].IsAttached())
		}

		// Detach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				err := clients[idx].Detach(ctx, channels[idx])
				if err != nil {
					t.Errorf("Failed to detach client %d: %v", idx, err)
				}
			}(i)
		}

		// Wait for all detachments using assert.Eventually
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if channels[i].IsAttached() {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All clients should be detached")

		// Verify all are detached
		for i := range clientCount {
			assert.False(t, channels[i].IsAttached())
		}

		// Note: Detached counters may not immediately reflect zero count
		// without active WatchChannel. This is expected behavior.
	})

	t.Run("mixed resource test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)

		cli := clients[0]

		// Create both document and session counter
		docKey := helper.TestKey(t, 1)
		channelKey := helper.TestKey(t, 2)

		doc := document.New(docKey)
		channel, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Test polymorphic usage
		resources := []attachable.Attachable{doc, channel}

		// Attach all resources
		for _, resource := range resources {
			err = cli.Attach(ctx, resource)
			require.NoError(t, err)
			assert.True(t, resource.IsAttached())
		}

		// Verify types
		assert.Equal(t, attachable.TypeDocument, doc.Type())
		assert.Equal(t, attachable.TypeChannel, channel.Type())

		// Detach all resources
		for _, resource := range resources {
			err = cli.Detach(ctx, resource)
			require.NoError(t, err)
			assert.False(t, resource.IsAttached())
		}
	})

	t.Run("channel TTL refresh test", func(t *testing.T) {
		// Use custom heartbeat interval for faster testing
		cli, err := client.Dial(
			defaultServer.RPCAddr(),
			client.WithChannelHeartbeatInterval(1*time.Second),
		)
		require.NoError(t, err)
		require.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Deactivate(ctx))
			assert.NoError(t, cli.Close())
		}()

		channelKey := helper.TestKey(t)
		ch, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Attach channel
		err = cli.Attach(ctx, ch)
		require.NoError(t, err)
		assert.True(t, ch.IsAttached())

		// Wait for a few heartbeat cycles (3 seconds)
		// The client should automatically send refresh requests via heartbeat
		time.Sleep(3 * time.Second)

		// Channel should still be active after multiple refresh cycles
		assert.True(t, ch.IsAttached())

		// Detach
		require.NoError(t, cli.Detach(ctx, ch))
		assert.False(t, ch.IsAttached())
	})

	t.Run("channel manual sync mode test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		client1, client2 := clients[0], clients[1]

		// Create channel counters for the same room
		channelKey := helper.TestKey(t)
		counter1, err := channel.New(channelKey)
		assert.NoError(t, err)
		counter2, err := channel.New(channelKey)
		assert.NoError(t, err)

		// Attach client1 with manual sync mode (no realtime option)
		err = client1.Attach(ctx, counter1)
		require.NoError(t, err)
		assert.Equal(t, int64(1), counter1.SessionCount())

		// Attach client2 with manual sync mode
		err = client2.Attach(ctx, counter2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), counter2.SessionCount())

		// In manual mode, counters don't automatically update
		// counter1 still shows old count
		assert.Equal(t, int64(1), counter1.SessionCount())

		// Manually sync counter1 to get updated count
		err = client1.Sync(ctx, client.WithKey(counter1.Key()))
		require.NoError(t, err)

		// Now counter1 should have the updated count
		assert.Equal(t, int64(2), counter1.SessionCount())

		// Detach client2
		require.NoError(t, client2.Detach(ctx, counter2))

		// counter1 still shows old count (manual mode)
		assert.Equal(t, int64(2), counter1.SessionCount())

		// Sync to get updated count
		err = client1.Sync(ctx, client.WithKey(counter1.Key()))
		require.NoError(t, err)

		// Now counter1 should show 1
		assert.Equal(t, int64(1), counter1.SessionCount())

		// Detach client1
		require.NoError(t, client1.Detach(ctx, counter1))
	})

	t.Run("channel sync all resources test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		client1, client2 := clients[0], clients[1]

		// Create document and channel
		doc1 := document.New(helper.TestKey(t, 0))
		counter1, err := channel.New(helper.TestKey(t, 1))
		assert.NoError(t, err)

		// Attach both resources in manual mode
		require.NoError(t, client1.Attach(ctx, doc1))
		require.NoError(t, client1.Attach(ctx, counter1))

		// Attach client2 to the same channel
		counter2, err := channel.New(helper.TestKey(t, 1))
		assert.NoError(t, err)
		require.NoError(t, client2.Attach(ctx, counter2))

		// counter1 shows old count (manual mode)
		assert.Equal(t, int64(1), counter1.SessionCount())

		// Sync all resources without specifying keys
		require.NoError(t, client1.Sync(ctx))

		// Now counter1 should have updated count
		assert.Equal(t, int64(2), counter1.SessionCount())

		// Cleanup
		require.NoError(t, client1.Detach(ctx, doc1))
		require.NoError(t, client1.Detach(ctx, counter1))
		require.NoError(t, client2.Detach(ctx, counter2))
	})

	t.Run("hierarchical path channel test", func(t *testing.T) {
		clients := activeClients(t, 5)
		defer deactivateAndCloseClients(t, clients)

		// Create hierarchical channel keys
		// room-1: 2 clients
		// room-1.section-1: 1 client
		// room-1.section-1.desk-1: 1 client
		// room-1.section-2: 1 client
		channelKeys := []string{
			"channel-test-hierarchical-room-1",
			"channel-test-hierarchical-room-1.section-1",
			"channel-test-hierarchical-room-1.section-1.desk-1",
			"channel-test-hierarchical-room-1.section-2",
			"channel-test-hierarchical-room-1",
		}

		channels := make([]*channel.Channel, len(clients))
		for i, client := range clients {
			var err error
			channels[i], err = channel.New(key.Key(channelKeys[i]))
			assert.NoError(t, err)
			err = client.Attach(ctx, channels[i])
			require.NoError(t, err)
		}

		// Wait for all attachments to complete
		time.Sleep(500 * time.Millisecond)

		// Verify counts
		// room-1 direct session counts (per-client view)
		assert.Equal(t, int64(1), channels[0].SessionCount(), "room-1 should have 1 direct session")
		assert.Equal(t, int64(1), channels[1].SessionCount(), "room-1.section-1 should have 1 direct sessions")
		assert.Equal(t, int64(1), channels[2].SessionCount(), "room-1.section-1.desk-1 should have 1 direct sessions")
		assert.Equal(t, int64(1), channels[3].SessionCount(), "room-1.section-2 should have 1 direct sessions")
		assert.Equal(t, int64(2), channels[4].SessionCount(), "room-1 should have 2 direct sessions")

		// Note: Client-side Count() only shows direct count for the exact path
		// The hierarchical counting (includeSubPath) is a server-side feature
		// and would need to be exposed through the API to test from client

		// Cleanup
		for i, client := range clients {
			require.NoError(t, client.Detach(ctx, channels[i]))
		}

		assert.Equal(t, int64(0), channels[0].SessionCount(), "room-1 should have 0 direct sessions after detach")
		assert.Equal(t, int64(0), channels[1].SessionCount(), "room-1.section-1 should have 0 direct sessions after detach")
		assert.Equal(t, int64(0), channels[2].SessionCount(), "room-1.section-1.desk-1 should have 0 direct sessions after detach")
		assert.Equal(t, int64(0), channels[3].SessionCount(), "room-1.section-2 should have 0 direct sessions after detach")
		assert.Equal(t, int64(0), channels[4].SessionCount(), "room-1 should have 0 direct sessions after detach")
	})

	t.Run("channel path cleanup test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		// Create nested path channels
		channelKeys := []string{
			"channel-test-cleanup-room-1",
			"channel-test-cleanup-room-1.section-1",
			"channel-test-cleanup-room-1.section-1.desk-1",
		}

		channels := make([]*channel.Channel, len(channelKeys))
		for i, k := range channelKeys {
			var err error
			channels[i], err = channel.New(key.Key(k))
			assert.NoError(t, err)
			err = clients[i].Attach(ctx, channels[i])
			require.NoError(t, err)
			assert.True(t, channels[i].IsAttached())
		}

		// Detach in reverse order (leaf to root)
		for i := len(channels) - 1; i >= 0; i-- {
			err := clients[i].Detach(ctx, channels[i])
			require.NoError(t, err)
			assert.False(t, channels[i].IsAttached())
		}

		// Re-attach and detach in different order
		for i, k := range channelKeys {
			var err error
			channels[i], err = channel.New(key.Key(k))
			assert.NoError(t, err)
			err = clients[i].Attach(ctx, channels[i])
			require.NoError(t, err)
		}

		// Detach root first (should not affect children)
		err := clients[0].Detach(ctx, channels[0])
		require.NoError(t, err)

		// Children should still be attached
		assert.True(t, channels[1].IsAttached())
		assert.True(t, channels[2].IsAttached())

		// Cleanup remaining
		for i := 1; i < len(channels); i++ {
			require.NoError(t, clients[i].Detach(ctx, channels[i]))
		}

		for _, counter := range channels {
			assert.False(t, counter.IsAttached())
		}
	})

	t.Run("multiple key paths concurrent operations test", func(t *testing.T) {
		clientCount := 9
		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		// Create channel keys with different path depths
		channelKeys := make([]string, clientCount)
		for i := range clientCount {
			switch i % 3 {
			case 0:
				channelKeys[i] = "channel-test-concurrent-operations-space-1"
			case 1:
				channelKeys[i] = "channel-test-concurrent-operations-space-1.room-1"
			case 2:
				channelKeys[i] = "channel-test-concurrent-operations-space-1.room-1.desk-1"
			}
		}

		channels := make([]*channel.Channel, clientCount)

		// Attach all concurrently
		var wg sync.WaitGroup
		for i := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				var err error
				channels[idx], err = channel.New(key.Key(channelKeys[idx]))
				assert.NoError(t, err)
				err = clients[idx].Attach(ctx, channels[idx])
				assert.NoError(t, err)
			}(i)
		}
		wg.Wait()

		// Wait for all to be attached
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if !channels[i].IsAttached() {
					return false
				}
			}
			return true
		}, 2*time.Second, 100*time.Millisecond)

		for _, channel := range channels {
			assert.True(t, channel.IsAttached())
		}

		// Detach all concurrently
		for i := range clientCount {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				err := clients[idx].Detach(ctx, channels[idx])
				assert.NoError(t, err)
			}(i)
		}
		wg.Wait()

		// Wait for all to be detached
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if channels[i].IsAttached() {
					return false
				}
			}
			return true
		}, 2*time.Second, 100*time.Millisecond)

		for _, channel := range channels {
			assert.False(t, channel.IsAttached())
			assert.Equal(t, int64(0), channel.SessionCount())
		}
	})
}
