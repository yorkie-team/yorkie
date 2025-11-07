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
		ch := channel.New(channelKey)

		// Test initial state
		assert.Equal(t, channelKey, ch.Key())
		assert.Equal(t, attachable.TypeChannel, ch.Type())
		assert.Equal(t, attachable.StatusDetached, ch.Status())
		assert.False(t, ch.IsAttached())

		// Attach channel
		err := cli.Attach(ctx, ch)
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
		ch1 := channel.New(channelKey)
		ch2 := channel.New(channelKey)
		ch3 := channel.New(channelKey)

		// Attach first client
		err := client1.Attach(ctx, ch1)
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

	t.Run("presence count value verification test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		cli := clients[0]

		// Create channel
		ch := channel.New(helper.TestKey(t))

		// Attach and start watching to get count updates
		require.NoError(t, cli.Attach(ctx, ch))

		countChan, closeWatch, err := cli.WatchChannel(ctx, ch)
		require.NoError(t, err)
		defer closeWatch()

		// Wait for initial count (should be 1 - just this client)
		var receivedCount int64
		assert.Eventually(t, func() bool {
			select {
			case count := <-countChan:
				receivedCount = count
				return true
			default:
				return false
			}
		}, time.Second, 50*time.Millisecond, "Should receive initial count")

		assert.Equal(t, int64(1), receivedCount, "Initial count should be 1")

		// Validate Counter object holds the actual count value
		assert.Equal(t, int64(1), ch.Count(), "Counter object should reflect actual count")
	})

	t.Run("presence count changes with multiple clients test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		watcherClient := clients[0]
		participantClients := clients[1:]

		// Create presence channels for the same room
		channelKey := helper.TestKey(t)
		watcher := channel.New(channelKey)

		// Attach watcher first
		err := watcherClient.Attach(ctx, watcher)
		require.NoError(t, err)

		// Start watching count changes
		countChan, closeWatch, err := watcherClient.WatchChannel(ctx, watcher)
		require.NoError(t, err)
		defer closeWatch()

		// Helper to collect and track latest count
		var latestCount int64 = -1
		collectCount := func() {
			for {
				select {
				case c := <-countChan:
					latestCount = c
					t.Logf("Received count update: %d", c)
				default:
					return
				}
			}
		}

		// Helper to get current latest count
		getLatestCount := func() int64 {
			collectCount()
			return latestCount
		}

		// Wait for initial count (1 = watcher) - blocking wait for first count
		assert.Eventually(t, func() bool {
			select {
			case count := <-countChan:
				return count == 1
			default:
				return false
			}
		}, time.Second, 50*time.Millisecond, "Should receive initial count of 1")

		// Now use getLatestCount to drain any additional counts
		getLatestCount()

		// Add participants one by one and verify count increases
		participants := make([]*channel.Channel, len(participantClients))
		for i, client := range participantClients {
			participants[i] = channel.New(channelKey)

			t.Logf("Attaching participant %d", i+1)
			err := client.Attach(ctx, participants[i])
			require.NoError(t, err)

			expectedCount := int64(2 + i) // watcher + participants so far
			t.Logf("Waiting for count to reach %d", expectedCount)
			assert.Eventually(t, func() bool {
				count := getLatestCount()
				t.Logf("Current count: %d, Expected: %d", count, expectedCount)
				return count == expectedCount
			}, 5*time.Second, 100*time.Millisecond, "Count should increase to %d", expectedCount)
		}

		// Final count should be 3 (1 watcher + 2 participants)
		t.Log("Checking final count should be 3")
		assert.Eventually(t, func() bool {
			count := getLatestCount()
			t.Logf("Final count check: %d (expected: 3)", count)
			return count == 3
		}, 5*time.Second, 100*time.Millisecond, "Final count should be 3")

		// Remove participants one by one and verify count decreases
		for i, client := range participantClients {
			err := client.Detach(ctx, participants[i])
			require.NoError(t, err)

			expectedCount := int64(3 - 1 - i) // remaining clients
			assert.Eventually(t, func() bool {
				count := getLatestCount()
				return count == expectedCount
			}, 2*time.Second, 50*time.Millisecond, "Count should decrease to %d", expectedCount)
		}

		// Final count should be 1 (only watcher remains)
		assert.Eventually(t, func() bool {
			count := getLatestCount()
			return count == 1
		}, time.Second, 50*time.Millisecond, "Final count should be 1")

		// Validate Counter object reflects the final count
		assert.Equal(t, int64(1), watcher.Count(), "Watcher counter should reflect final count")
	})

	t.Run("out-of-order event handling test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		// Create presence channels for two clients
		channelKey := helper.TestKey(t)
		ch1 := channel.New(channelKey)
		ch2 := channel.New(channelKey)

		// Attach the first counter first
		err := clients[0].Attach(ctx, ch1)
		require.NoError(t, err)
		defer func() { _ = clients[0].Detach(ctx, ch1) }()

		// Now we can watch from the attached channel
		countCh, cancelWatch, err := clients[0].WatchChannel(ctx, ch1)
		require.NoError(t, err)
		defer cancelWatch()

		// Attach the second channel
		err = clients[1].Attach(ctx, ch2)
		require.NoError(t, err)
		defer func() { _ = clients[1].Detach(ctx, ch2) }()

		// Track the last received count
		var lastCount int64

		// Verify initial count (2 clients)
		assert.Eventually(t, func() bool {
			select {
			case count := <-countCh:
				lastCount = count
				return count == 2
			default:
				return false
			}
		}, 5*time.Second, 100*time.Millisecond)

		// Sequential attach/detach operations with proper waiting
		// to ensure presence state is properly synchronized
		for i := 0; i < 5; i++ {
			// Detach
			err = clients[1].Detach(ctx, ch2)
			require.NoError(t, err)

			// Wait for count to stabilize at 1
			stabilized := assert.Eventually(t, func() bool {
				select {
				case count := <-countCh:
					lastCount = count
					return count == 1
				case <-time.After(10 * time.Millisecond):
					return false
				}
			}, time.Second, 50*time.Millisecond)

			if !stabilized {
				t.Logf("Warning: count did not stabilize to 1 after detach iteration %d", i)
			}

			// Re-attach
			err = clients[1].Attach(ctx, ch2)
			require.NoError(t, err)

			// Wait for count to stabilize at 2
			stabilized = assert.Eventually(t, func() bool {
				select {
				case count := <-countCh:
					lastCount = count
					return count == 2
				case <-time.After(10 * time.Millisecond):
					return false
				}
			}, time.Second, 50*time.Millisecond)

			if !stabilized {
				t.Logf("Warning: count did not stabilize to 2 after attach iteration %d", i)
			}
		}

		// Drain any remaining events
		time.Sleep(100 * time.Millisecond)
		draining := true
		for draining {
			select {
			case count := <-countCh:
				lastCount = count
				t.Logf("Drained count: %d", count)
			case <-time.After(50 * time.Millisecond):
				draining = false
			}
		}

		// Final count should be 2 (both attached)
		require.Equal(t, int64(2), lastCount, "Final count should be 2 after all operations")

		// Note: Counter objects only reflect server state if they have WatchChannel active
		// counter1 has WatchChannel active, so it should reflect the count
		assert.Equal(t, int64(2), ch1.Count(), "Counter1 should reflect final count")
		// counter2 doesn't have WatchChannel, so we can't verify its internal count
		// This is expected behavior - Counter objects need WatchChannel to stay synchronized
	})

	t.Run("concurrent count consistency test", func(t *testing.T) {
		clientCount := 10
		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		channelKey := helper.TestKey(t)
		channels := make([]*channel.Channel, clientCount)

		// Create channels for all clients
		for i := range clientCount {
			channels[i] = channel.New(channelKey)
		}

		// Attach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				assert.NoError(t, clients[idx].Attach(ctx, channels[idx]))
			}(i)
		}

		// Wait for all to be attached
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if !channels[i].IsAttached() {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All clients should be attached")

		// Start watching from multiple clients and verify they see the same count
		watchClients := clients[:3] // Use first 3 clients as watchers
		countChans := make([]<-chan int64, len(watchClients))
		closeWatches := make([]func(), len(watchClients))

		for i, client := range watchClients {
			countChan, closeWatch, err := client.WatchChannel(ctx, channels[i])
			require.NoError(t, err)
			countChans[i] = countChan
			closeWatches[i] = closeWatch
		}

		defer func() {
			for _, closeWatch := range closeWatches {
				closeWatch()
			}
		}()

		// Collect counts from all watchers
		collectCounts := func() []int64 {
			counts := make([]int64, len(watchClients))
			for i, countChan := range countChans {
				select {
				case count := <-countChan:
					counts[i] = count
				default:
					// Keep previous count if no new update
				}
			}
			return counts
		}

		// Eventually all watchers should see count = clientCount
		assert.Eventually(t, func() bool {
			counts := collectCounts()
			for _, count := range counts {
				if count != int64(clientCount) {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All watchers should see count = %d", clientCount)

		t.Logf("All %d watchers consistently see count = %d", len(watchClients), clientCount)
	})

	t.Run("channel watch integration test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		watcherClient, participantClient := clients[0], clients[1]

		// Create channels
		channelKey := helper.TestKey(t)
		watcher := channel.New(channelKey)
		participant := channel.New(channelKey)

		// Attach watcher first
		err := watcherClient.Attach(ctx, watcher)
		require.NoError(t, err)

		// Start watching presence changes
		countChan, closeWatch, err := watcherClient.WatchChannel(ctx, watcher)
		require.NoError(t, err)
		defer closeWatch()

		// Collect count updates in a slice
		var counts []int64

		// Helper function to collect available counts
		collectCounts := func() {
			for {
				select {
				case count, ok := <-countChan:
					if !ok {
						return
					}
					counts = append(counts, count)
				default:
					return
				}
			}
		}

		// Wait for initial count
		assert.Eventually(t, func() bool {
			collectCounts()
			return len(counts) > 0
		}, time.Second, 50*time.Millisecond, "Should receive initial count")

		initialCountLen := len(counts)

		// Attach participant (should trigger count update)
		err = participantClient.Attach(ctx, participant)
		require.NoError(t, err)

		// Wait for count update after attach
		assert.Eventually(t, func() bool {
			collectCounts()
			return len(counts) > initialCountLen
		}, time.Second, 50*time.Millisecond, "Should receive count update after participant attach")

		attachCountLen := len(counts)

		// Detach participant (should trigger another count update)
		err = participantClient.Detach(ctx, participant)
		require.NoError(t, err)

		// Wait for count update after detach
		assert.Eventually(t, func() bool {
			collectCounts()
			return len(counts) > attachCountLen
		}, time.Second, 50*time.Millisecond, "Should receive count update after participant detach")

		// Verify we received count updates
		assert.GreaterOrEqual(t, len(counts), 3, "Should receive at least 3 count updates (initial, attach, detach)")
		t.Logf("Received count updates: %v", counts)
	})

	t.Run("channel stress test", func(t *testing.T) {
		clientCount := 5

		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		channelKey := helper.TestKey(t)
		channels := make([]*channel.Channel, clientCount)

		// Create and activate multiple clients
		for i := range clientCount {
			channels[i] = channel.New(channelKey)
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

		// Create both document and presence counter
		docKey := helper.TestKey(t, 1)
		channelKey := helper.TestKey(t, 2)

		doc := document.New(docKey)
		channel := channel.New(channelKey)

		// Test polymorphic usage
		resources := []attachable.Attachable{doc, channel}

		// Attach all resources
		var err error
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
		ch := channel.New(channelKey)

		// Attach presence counter
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

	t.Run("channel expires after TTL without refresh", func(t *testing.T) {
		// This test verifies that channel sessions expire when clients don't send refresh.
		// We use a short TTL (5s) configured in test helper.
		ctx := context.Background()

		// cli1 has heartbeat disabled (long interval) to simulate a crash.
		cli1, err := client.Dial(defaultServer.RPCAddr(), client.WithChannelHeartbeatInterval(1*time.Hour))
		require.NoError(t, err)
		require.NoError(t, cli1.Activate(ctx))
		defer func() { assert.NoError(t, cli1.Close()) }()

		// cli2 has normal heartbeat to stay active and observe cli1's expiration.
		cli2, err := client.Dial(defaultServer.RPCAddr(), client.WithChannelHeartbeatInterval(2*time.Second))
		require.NoError(t, err)
		require.NoError(t, cli2.Activate(ctx))
		defer func() { assert.NoError(t, cli2.Close()) }()

		// Use the same presence key for both clients
		channelKey := helper.TestKey(t)
		counter1 := channel.New(channelKey)
		counter2 := channel.New(channelKey)
		require.NoError(t, cli1.Attach(ctx, counter1, client.WithChannelRealtimeSync()))
		require.NoError(t, cli2.Attach(ctx, counter2, client.WithChannelRealtimeSync()))

		// Start watching on cli2 to receive count updates
		countCh, unwatch, err := cli2.WatchChannel(ctx, counter2)
		require.NoError(t, err)
		defer unwatch()

		// Wait for initial count
		select {
		case count := <-countCh:
			assert.Equal(t, int64(2), count, "Initial count should be 2")
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for initial count")
		}

		// Simulate cli1 crash by deactivating without detach
		// This stops the client but leaves presence on server
		require.NoError(t, cli1.Deactivate(ctx))

		// Wait for TTL (5s) + cleanup interval (1s) + buffer
		// After cleanup runs, cli2 should receive an update with count=1
		dropped := false
		for !dropped {
			select {
			case count := <-countCh:
				if count == 1 {
					dropped = true
					t.Logf("Count dropped to 1 after client1 expired")
				}
			case <-time.After(8 * time.Second):
				t.Fatal("Timeout waiting for count to drop after TTL expiration")
			}
		}

		// Verify count dropped to 1
		assert.Equal(t, int64(1), counter2.Count(), "Count should be 1 after client1 expired")

		// client2 should still be active
		assert.True(t, counter2.IsAttached())
	})

	t.Run("presence manual sync mode test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		client1, client2 := clients[0], clients[1]

		// Create presence counters for the same room
		channelKey := helper.TestKey(t)
		counter1 := channel.New(channelKey)
		counter2 := channel.New(channelKey)

		// Attach client1 with manual sync mode (no realtime option)
		err := client1.Attach(ctx, counter1)
		require.NoError(t, err)
		assert.Equal(t, int64(1), counter1.Count())

		// Attach client2 with manual sync mode
		err = client2.Attach(ctx, counter2)
		require.NoError(t, err)
		assert.Equal(t, int64(2), counter2.Count())

		// In manual mode, counters don't automatically update
		// counter1 still shows old count
		assert.Equal(t, int64(1), counter1.Count())

		// Manually sync counter1 to get updated count
		err = client1.Sync(ctx, client.WithKey(counter1.Key()))
		require.NoError(t, err)

		// Now counter1 should have the updated count
		assert.Equal(t, int64(2), counter1.Count())

		// Detach client2
		require.NoError(t, client2.Detach(ctx, counter2))

		// counter1 still shows old count (manual mode)
		assert.Equal(t, int64(2), counter1.Count())

		// Sync to get updated count
		err = client1.Sync(ctx, client.WithKey(counter1.Key()))
		require.NoError(t, err)

		// Now counter1 should show 1
		assert.Equal(t, int64(1), counter1.Count())

		// Detach client1
		require.NoError(t, client1.Detach(ctx, counter1))
	})

	t.Run("presence realtime sync mode test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		client1, client2 := clients[0], clients[1]

		// Create presence counters for the same room
		channelKey := helper.TestKey(t)
		ch1 := channel.New(channelKey)
		ch2 := channel.New(channelKey)

		// Attach client1 with realtime sync mode
		err := client1.Attach(ctx, ch1, client.WithChannelRealtimeSync())
		require.NoError(t, err)
		assert.Equal(t, int64(1), ch1.Count())

		// Start watching for count changes
		countChan1, closeWatch1, err := client1.WatchChannel(ctx, ch1)
		require.NoError(t, err)
		defer closeWatch1()

		// Wait for initial count
		select {
		case count := <-countChan1:
			assert.Equal(t, int64(1), count)
		case <-time.After(3 * time.Second):
			t.Fatal("Timeout waiting for initial count")
		}

		// Attach client2 with realtime sync mode
		err = client2.Attach(ctx, ch2, client.WithChannelRealtimeSync())
		require.NoError(t, err)

		// client1 should receive updated count automatically
		select {
		case count := <-countChan1:
			assert.Equal(t, int64(2), count)
			assert.Equal(t, int64(2), ch1.Count())
		case <-time.After(3 * time.Second):
			t.Fatal("Timeout waiting for count update after client2 attached")
		}

		// Detach client2
		require.NoError(t, client2.Detach(ctx, ch2))

		// client1 should receive updated count automatically
		select {
		case count := <-countChan1:
			assert.Equal(t, int64(1), count)
			assert.Equal(t, int64(1), ch1.Count())
		case <-time.After(3 * time.Second):
			t.Fatal("Timeout waiting for count update after client2 detached")
		}

		// Detach client1
		require.NoError(t, client1.Detach(ctx, ch1))
	})

	t.Run("presence sync all resources test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		client1, client2 := clients[0], clients[1]

		// Create document and presence counter
		doc1 := document.New(helper.TestKey(t, 0))
		counter1 := channel.New(helper.TestKey(t, 1))

		// Attach both resources in manual mode
		require.NoError(t, client1.Attach(ctx, doc1))
		require.NoError(t, client1.Attach(ctx, counter1))

		// Attach client2 to the same presence
		counter2 := channel.New(helper.TestKey(t, 1))
		require.NoError(t, client2.Attach(ctx, counter2))

		// counter1 shows old count (manual mode)
		assert.Equal(t, int64(1), counter1.Count())

		// Sync all resources without specifying keys
		require.NoError(t, client1.Sync(ctx))

		// Now counter1 should have updated count
		assert.Equal(t, int64(2), counter1.Count())

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
			channels[i] = channel.New(key.Key(channelKeys[i]))
			err := client.Attach(ctx, channels[i])
			require.NoError(t, err)
		}

		// Wait for all attachments to complete
		time.Sleep(500 * time.Millisecond)

		// Verify counts
		// room-1 should have 2 direct presences
		assert.Equal(t, int64(1), channels[0].Count(), "room-1 should have 1 direct presences")
		assert.Equal(t, int64(1), channels[1].Count(), "room-1.section-1 should have 1 direct presences")
		assert.Equal(t, int64(1), channels[2].Count(), "room-1.section-1.desk-1 should have 1 direct presences")
		assert.Equal(t, int64(1), channels[3].Count(), "room-1.section-2 should have 1 direct presences")
		assert.Equal(t, int64(2), channels[4].Count(), "room-1 should have 2 direct presences")

		// Note: Client-side Count() only shows direct count for the exact path
		// The hierarchical counting (includeSubPath) is a server-side feature
		// and would need to be exposed through the API to test from client

		// Cleanup
		for i, client := range clients {
			require.NoError(t, client.Detach(ctx, channels[i]))
		}

		assert.Equal(t, int64(0), channels[0].Count(), "room-1 should have 1 direct presences")
		assert.Equal(t, int64(0), channels[1].Count(), "room-1.section-1 should have 1 direct presences")
		assert.Equal(t, int64(0), channels[2].Count(), "room-1.section-1.desk-1 should have 1 direct presences")
		assert.Equal(t, int64(0), channels[3].Count(), "room-1.section-2 should have 1 direct presences")
		assert.Equal(t, int64(0), channels[4].Count(), "room-1 should have 2 direct presences")
	})

	t.Run("channel path cleanup test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		// Create nested path presences
		channelKeys := []string{
			"channel-test-cleanup-room-1",
			"channel-test-cleanup-room-1.section-1",
			"channel-test-cleanup-room-1.section-1.desk-1",
		}

		channels := make([]*channel.Channel, len(channelKeys))
		for i, k := range channelKeys {
			channels[i] = channel.New(key.Key(k))
			err := clients[i].Attach(ctx, channels[i])
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
			channels[i] = channel.New(key.Key(k))
			err := clients[i].Attach(ctx, channels[i])
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

		// Create presence keys with different path depths
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
				channels[idx] = channel.New(key.Key(channelKeys[idx]))
				err := clients[idx].Attach(ctx, channels[idx])
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
			assert.Equal(t, int64(0), channel.Count())
		}
	})
}
