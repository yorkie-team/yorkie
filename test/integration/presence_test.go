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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPresenceIntegration(t *testing.T) {
	ctx := context.Background()

	t.Run("single client presence counter test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)

		cli := clients[0]

		// Create presence counter
		presenceKey := helper.TestDocKey(t)
		counter := presence.New(presenceKey)

		// Test initial state
		assert.Equal(t, presenceKey, counter.Key())
		assert.Equal(t, attachable.TypePresence, counter.Type())
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())

		// Attach presence counter
		err := cli.Attach(ctx, counter)
		require.NoError(t, err)

		// Verify attached state
		assert.Equal(t, attachable.StatusAttached, counter.Status())
		assert.True(t, counter.IsAttached())

		// Detach presence counter
		require.NoError(t, cli.Detach(ctx, counter))

		// Verify detached state
		assert.Equal(t, attachable.StatusDetached, counter.Status())
		assert.False(t, counter.IsAttached())
	})

	t.Run("multiple clients presence counter test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		client1, client2, client3 := clients[0], clients[1], clients[2]

		// Create presence counters for the same room
		presenceKey := helper.TestDocKey(t)
		counter1 := presence.New(presenceKey)
		counter2 := presence.New(presenceKey)
		counter3 := presence.New(presenceKey)

		// Attach first client
		err := client1.Attach(ctx, counter1)
		require.NoError(t, err)

		// Attach second client
		err = client2.Attach(ctx, counter2)
		require.NoError(t, err)

		// Attach third client
		err = client3.Attach(ctx, counter3)
		require.NoError(t, err)

		// All should be attached
		assert.True(t, counter1.IsAttached())
		assert.True(t, counter2.IsAttached())
		assert.True(t, counter3.IsAttached())

		// Detach clients
		require.NoError(t, client1.Detach(ctx, counter1))
		assert.False(t, counter1.IsAttached())
		require.NoError(t, client2.Detach(ctx, counter2))
		assert.False(t, counter2.IsAttached())
		require.NoError(t, client3.Detach(ctx, counter3))
		assert.False(t, counter3.IsAttached())
	})

	t.Run("presence count value verification test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		cli := clients[0]

		// Create presence counter
		counter := presence.New(helper.TestDocKey(t))

		// Attach and start watching to get count updates
		require.NoError(t, cli.Attach(ctx, counter))

		countChan, closeWatch, err := cli.WatchPresence(ctx, counter)
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
		assert.Equal(t, int64(1), counter.Count(), "Counter object should reflect actual count")
	})

	t.Run("presence count changes with multiple clients test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)

		watcherClient := clients[0]
		participantClients := clients[1:]

		// Create presence counters for the same room
		presenceKey := helper.TestDocKey(t)
		watcherCounter := presence.New(presenceKey)

		// Attach watcher first
		err := watcherClient.Attach(ctx, watcherCounter)
		require.NoError(t, err)

		// Start watching count changes
		countChan, closeWatch, err := watcherClient.WatchPresence(ctx, watcherCounter)
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
		participantCounters := make([]*presence.Counter, len(participantClients))
		for i, client := range participantClients {
			participantCounters[i] = presence.New(presenceKey)

			t.Logf("Attaching participant %d", i+1)
			err := client.Attach(ctx, participantCounters[i])
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
			err := client.Detach(ctx, participantCounters[i])
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
		assert.Equal(t, int64(1), watcherCounter.Count(), "Watcher counter should reflect final count")
	})

	t.Run("out-of-order event handling test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		// Create presence counters for two clients
		presenceKey := helper.TestDocKey(t)
		counter1 := presence.New(presenceKey)
		counter2 := presence.New(presenceKey)

		// Attach the first counter first
		err := clients[0].Attach(ctx, counter1)
		require.NoError(t, err)
		defer func() { _ = clients[0].Detach(ctx, counter1) }()

		// Now we can watch from the attached counter
		countCh, cancelWatch, err := clients[0].WatchPresence(ctx, counter1)
		require.NoError(t, err)
		defer cancelWatch()

		// Attach the second counter
		err = clients[1].Attach(ctx, counter2)
		require.NoError(t, err)
		defer func() { _ = clients[1].Detach(ctx, counter2) }()

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
			err = clients[1].Detach(ctx, counter2)
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
			err = clients[1].Attach(ctx, counter2)
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

		// Note: Counter objects only reflect server state if they have WatchPresence active
		// counter1 has WatchPresence active, so it should reflect the count
		assert.Equal(t, int64(2), counter1.Count(), "Counter1 should reflect final count")
		// counter2 doesn't have WatchPresence, so we can't verify its internal count
		// This is expected behavior - Counter objects need WatchPresence to stay synchronized
	})

	t.Run("concurrent count consistency test", func(t *testing.T) {
		clientCount := 10
		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		presenceKey := helper.TestDocKey(t)
		counters := make([]*presence.Counter, clientCount)

		// Create counters for all clients
		for i := range clientCount {
			counters[i] = presence.New(presenceKey)
		}

		// Attach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				assert.NoError(t, clients[idx].Attach(ctx, counters[idx]))
			}(i)
		}

		// Wait for all to be attached
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if !counters[i].IsAttached() {
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
			countChan, closeWatch, err := client.WatchPresence(ctx, counters[i])
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

	t.Run("presence watch integration test", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)

		watcherClient, participantClient := clients[0], clients[1]

		// Create presence counters
		presenceKey := helper.TestDocKey(t)
		watcherCounter := presence.New(presenceKey)
		participantCounter := presence.New(presenceKey)

		// Attach watcher first
		err := watcherClient.Attach(ctx, watcherCounter)
		require.NoError(t, err)

		// Start watching presence changes
		countChan, closeWatch, err := watcherClient.WatchPresence(ctx, watcherCounter)
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
		err = participantClient.Attach(ctx, participantCounter)
		require.NoError(t, err)

		// Wait for count update after attach
		assert.Eventually(t, func() bool {
			collectCounts()
			return len(counts) > initialCountLen
		}, time.Second, 50*time.Millisecond, "Should receive count update after participant attach")

		attachCountLen := len(counts)

		// Detach participant (should trigger another count update)
		err = participantClient.Detach(ctx, participantCounter)
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

	t.Run("presence counter stress test", func(t *testing.T) {
		clientCount := 5

		clients := activeClients(t, clientCount)
		defer deactivateAndCloseClients(t, clients)

		presenceKey := helper.TestDocKey(t)
		counters := make([]*presence.Counter, clientCount)

		// Create and activate multiple clients
		for i := range clientCount {
			counters[i] = presence.New(presenceKey)
		}

		// Attach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				if err := clients[idx].Attach(ctx, counters[idx]); err != nil {
					t.Errorf("Failed to attach client %d: %v", idx, err)
				}
			}(i)
		}

		// Wait for all attachments using assert.Eventually
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if !counters[i].IsAttached() {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All clients should be attached")

		// Verify all are attached
		for i := range clientCount {
			assert.True(t, counters[i].IsAttached())
		}

		// Detach all clients concurrently
		for i := range clientCount {
			go func(idx int) {
				err := clients[idx].Detach(ctx, counters[idx])
				if err != nil {
					t.Errorf("Failed to detach client %d: %v", idx, err)
				}
			}(i)
		}

		// Wait for all detachments using assert.Eventually
		assert.Eventually(t, func() bool {
			for i := range clientCount {
				if counters[i].IsAttached() {
					return false
				}
			}
			return true
		}, time.Second, 100*time.Millisecond, "All clients should be detached")

		// Verify all are detached
		for i := range clientCount {
			assert.False(t, counters[i].IsAttached())
		}

		// Note: Detached counters may not immediately reflect zero count
		// without active WatchPresence. This is expected behavior.
	})

	t.Run("mixed resource test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)

		cli := clients[0]

		// Create both document and presence counter
		docKey := helper.TestDocKey(t, 1)
		presenceKey := helper.TestDocKey(t, 2)

		doc := document.New(docKey)
		counter := presence.New(presenceKey)

		// Test polymorphic usage
		resources := []attachable.Attachable{doc, counter}

		// Attach all resources
		var err error
		for _, resource := range resources {
			err = cli.Attach(ctx, resource)
			require.NoError(t, err)
			assert.True(t, resource.IsAttached())
		}

		// Verify types
		assert.Equal(t, attachable.TypeDocument, doc.Type())
		assert.Equal(t, attachable.TypePresence, counter.Type())

		// Detach all resources
		for _, resource := range resources {
			err = cli.Detach(ctx, resource)
			require.NoError(t, err)
			assert.False(t, resource.IsAttached())
		}
	})
}
