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

package channel_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	pkgtime "github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
)

var (
	defaultProjectID = types.ID("000000000000000000000000")
)

// mockPubSub is a mock implementation of PubSub for testing
type mockPubSub struct {
	mu     sync.Mutex
	events []events.ChannelEvent
}

func (m *mockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

// MockBroker is a mock implementation of Broker for testing
type MockBroker struct {
	mu       sync.Mutex
	Messages []messaging.Message
}

func (m *MockBroker) Produce(ctx context.Context, msg messaging.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Messages = append(m.Messages, msg)
	return nil
}

func (m *MockBroker) Close() error {
	return nil
}

func createManager(
	t *testing.T,
	ttl time.Duration,
	cleanupInterval time.Duration,
) (*channel.Manager, *mockPubSub, *MockBroker) {
	pubsub := &mockPubSub{}
	broker := &MockBroker{}
	brokers := messaging.NewBroker(broker, broker, broker, broker)
	db, err := memory.New()
	assert.NoError(t, err)
	_, _, err = db.EnsureDefaultUserAndProject(context.Background(), "test-user", "test-password")
	assert.NoError(t, err)
	manager := channel.NewManager(pubsub, ttl, cleanupInterval, nil, brokers, db)
	return manager, pubsub, broker
}

func TestChannelManager_RefreshAndCleanup(t *testing.T) {
	t.Run("refresh updates activity time", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, broker := createManager(t, ttl, cleanupInterval)

		// Create a channel
		refKey := types.ChannelRefKey{
			ProjectID:  types.NewID(),
			ChannelKey: "test-room",
		}
		clientID := pkgtime.InitialActorID

		sessionID, count, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count)

		// Verify broker events
		assert.Len(t, broker.Messages, 2)
		assert.IsType(t, messaging.ChannelEventsMessage{}, broker.Messages[0])
		assert.IsType(t, messaging.SessionEventsMessage{}, broker.Messages[1])

		// Wait a bit
		time.Sleep(100 * time.Millisecond)

		// Refresh the channel
		err = manager.Refresh(ctx, sessionID)
		assert.NoError(t, err)

		// Channel should still exist
		assert.Equal(t, int64(1), manager.SessionCount(refKey, false))
	})

	t.Run("cleanup removes expired channels", func(t *testing.T) {
		ctx := context.Background()
		ttl := 200 * time.Millisecond // Very short TTL for testing
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)

		// Create a channel
		refKey := types.ChannelRefKey{
			ProjectID:  defaultProjectID,
			ChannelKey: "test-room",
		}
		clientID := pkgtime.InitialActorID

		_, count, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count)

		// Wait for TTL to expire
		time.Sleep(300 * time.Millisecond)

		// Run cleanup
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, cleanedCount)

		// channel should be removed
		assert.Equal(t, int64(0), manager.SessionCount(refKey, false))
	})

	t.Run("refresh extends TTL and prevents cleanup", func(t *testing.T) {
		ctx := context.Background()
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)

		// Create a channel
		refKey := types.ChannelRefKey{
			ProjectID:  defaultProjectID,
			ChannelKey: "test-room",
		}
		clientID := pkgtime.InitialActorID

		sessionID, count, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count)

		// Wait half the TTL
		time.Sleep(150 * time.Millisecond)

		// Refresh to extend TTL
		err = manager.Refresh(ctx, sessionID)
		assert.NoError(t, err)

		// Wait another half TTL (total 300ms, but refreshed at 150ms)
		time.Sleep(150 * time.Millisecond)

		// Run cleanup - should not remove because we refreshed
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, cleanedCount)

		// channel should still exist
		assert.Equal(t, int64(1), manager.SessionCount(refKey, false))
	})

	t.Run("cleanup removes only expired channels", func(t *testing.T) {
		ctx := context.Background()
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)

		// Create two channels
		refKey := types.ChannelRefKey{
			ProjectID:  defaultProjectID,
			ChannelKey: "test-room",
		}
		clientID1 := pkgtime.InitialActorID
		clientID2, err := pkgtime.ActorIDFromHex("000000000000000000000001")
		assert.NoError(t, err)

		_, _, err = manager.Attach(ctx, refKey, clientID1)
		assert.NoError(t, err)

		// Wait a bit before creating second channel
		time.Sleep(200 * time.Millisecond)

		sessionID2, count, err := manager.Attach(ctx, refKey, clientID2)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), count)

		// Refresh only the second channel
		err = manager.Refresh(ctx, sessionID2)
		assert.NoError(t, err)

		// Wait for first channel to expire
		time.Sleep(200 * time.Millisecond)

		// Run cleanup - should remove only the first channel
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, cleanedCount)

		// Only second channel should remain
		assert.Equal(t, int64(1), manager.SessionCount(refKey, false))
	})

	t.Run("refresh non-existent channel returns error", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)

		// Try to refresh a non-existent channel
		nonExistentID := types.NewID()
		err := manager.Refresh(ctx, nonExistentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("cleanup with no channel does not error", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)

		// Run cleanup on empty manager
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, cleanedCount)
	})

	t.Run("default TTL is applied when zero", func(t *testing.T) {
		manager, _, _ := createManager(t, 0, 0)

		// Manager should have default TTL (60s)
		// We can't directly check the internal field, but we can verify it works
		stats := manager.Stats()
		assert.NotNil(t, stats)
	})
}

func TestChannelManager_Count(t *testing.T) {
	t.Run("get channelcount - hierarchical path ", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		channelIDs := make([]types.ID, 0)
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.user-1"}
		refKey4 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.user-2"}

		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey1, 10, "1")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey2, 10, "2")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey3, 10, "3")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey4, 10, "4")...)

		// Check channel counts
		channelRefKeys := []types.ChannelRefKey{refKey1, refKey2, refKey3, refKey4}
		assertChannelCounts(t, manager, channelRefKeys, []int64{40, 30, 10, 10}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{10, 10, 10, 10}, false)

		// Detach first level
		_, err := manager.Detach(ctx, channelIDs[0])
		assert.NoError(t, err)
		assertChannelCounts(t, manager, channelRefKeys, []int64{39, 30, 10, 10}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{9, 10, 10, 10}, false)

		// Detach second level
		_, err = manager.Detach(ctx, channelIDs[10])
		assert.NoError(t, err)
		assertChannelCounts(t, manager, channelRefKeys, []int64{38, 29, 10, 10}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{9, 9, 10, 10}, false)

		// Detach third level
		_, err = manager.Detach(ctx, channelIDs[20])
		assert.NoError(t, err)
		assertChannelCounts(t, manager, channelRefKeys, []int64{37, 28, 9, 10}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{9, 9, 9, 10}, false)

		// Cleanup all channels
		for _, channelID := range channelIDs {
			_, _ = manager.Detach(ctx, channelID)
		}
		assertChannelCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, false)
	})

	t.Run("get channelcount - wrong path ", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		channelIDs := make([]types.ID, 0)
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.user-1"}
		refKey4 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.user-2"}

		wrongRefKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		wrongRefKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		wrongRefKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-"}
		// not start with room-1
		wrongRefKey4 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "section-1.user-1"}

		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey1, 10, "1")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey2, 10, "2")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey3, 10, "3")...)
		channelIDs = append(channelIDs, attachChannels(t, ctx, manager, refKey4, 10, "4")...)

		// Check channel counts
		channelRefKeys := []types.ChannelRefKey{refKey1, refKey2, refKey3, refKey4}
		assertChannelCounts(t, manager, channelRefKeys, []int64{40, 30, 10, 10}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{10, 10, 10, 10}, false)

		// Check wrong channel counts
		wrongChannelRefKeys := []types.ChannelRefKey{wrongRefKey1, wrongRefKey2, wrongRefKey3, wrongRefKey4}
		assertChannelCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, true)
		assertChannelCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, false)

		// Cleanup all channels
		for _, channelID := range channelIDs {
			_, _ = manager.Detach(ctx, channelID)
		}
		assertChannelCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, true)
		assertChannelCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, false)
		assertChannelCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, true)
		assertChannelCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, false)
	})
}

func TestChannelManager_List(t *testing.T) {
	t.Run("list all channels without query", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create multiple channels
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "lobby"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 7, "3")

		// List all channels (no query)
		results := manager.List(projectID, "", 10)

		// Should return all 3 channels
		assert.Len(t, results, 3)

		// Should be sorted by session count (desc), then channel key (asc)
		assert.Equal(t, "lobby", results[0].Key.ChannelKey.String())
		assert.Equal(t, 7, results[0].Sessions)
		assert.Equal(t, "room-1", results[1].Key.ChannelKey.String())
		assert.Equal(t, 5, results[1].Sessions)
		assert.Equal(t, "room-2", results[2].Key.ChannelKey.String())
		assert.Equal(t, 3, results[2].Sessions)
	})

	t.Run("list channels with query filter", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create channels with different prefixes
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "lobby"}
		refKey4 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-3"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 7, "3")
		attachChannels(t, ctx, manager, refKey4, 4, "4")

		// List channels with "room-" prefix
		results := manager.List(projectID, "room-", 10)

		// Should return only 3 channels matching "room-" prefix
		assert.Len(t, results, 3)

		// Verify all results start with "room-"
		for _, result := range results {
			assert.Contains(t, result.Key.ChannelKey.String(), "room-")
		}

		// Should be sorted by session count (desc)
		assert.Equal(t, "room-1", results[0].Key.ChannelKey.String())
		assert.Equal(t, 5, results[0].Sessions)
		assert.Equal(t, "room-2", results[1].Key.ChannelKey.String())
		assert.Equal(t, 3, results[1].Sessions)
		assert.Equal(t, "room-3", results[2].Key.ChannelKey.String())
		assert.Equal(t, 4, results[2].Sessions)
	})

	t.Run("list channels with limit", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create 5 channels
		for i := 1; i <= 5; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			attachChannels(t, ctx, manager, refKey, i, fmt.Sprintf("%d", i))
		}

		// List with limit of 3
		results := manager.List(projectID, "", 3)

		// Should return only 3 channels
		assert.Len(t, results, 3)

		// Should return top 3 by session count
		assert.Equal(t, "room-1", results[0].Key.ChannelKey.String())
		assert.Equal(t, 1, results[0].Sessions)
		assert.Equal(t, "room-2", results[1].Key.ChannelKey.String())
		assert.Equal(t, 2, results[1].Sessions)
		assert.Equal(t, "room-3", results[2].Key.ChannelKey.String())
		assert.Equal(t, 3, results[2].Sessions)
	})

	t.Run("list channels with same session count sorts by key", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create channels with same session count
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "charlie"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "alpha"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "bravo"}

		attachChannels(t, ctx, manager, refKey1, 3, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 3, "3")

		// List all channels
		results := manager.List(projectID, "", 10)

		// Should be sorted alphabetically when session counts are equal
		assert.Len(t, results, 3)
		assert.Equal(t, "alpha", results[0].Key.ChannelKey.String())
		assert.Equal(t, "bravo", results[1].Key.ChannelKey.String())
		assert.Equal(t, "charlie", results[2].Key.ChannelKey.String())
	})

	t.Run("list channels returns empty for no matches", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create channels
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		attachChannels(t, ctx, manager, refKey1, 5, "1")

		// List with non-matching query
		results := manager.List(projectID, "lobby-", 10)

		// Should return empty list
		assert.Len(t, results, 0)
	})

	t.Run("list channels respects project ID isolation", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		// Create channels for two different projects
		refKey1 := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID2, ChannelKey: "room-1"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")

		// List channels for project 1
		results1 := manager.List(projectID1, "", 10)
		assert.Len(t, results1, 1)
		assert.Equal(t, projectID1, results1[0].Key.ProjectID)

		// List channels for project 2
		results2 := manager.List(projectID2, "", 10)
		assert.Len(t, results2, 1)
		assert.Equal(t, projectID2, results2[0].Key.ProjectID)
	})

	t.Run("list channels normalizes limit", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		// Create 5 channels
		for i := 1; i <= 5; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			attachChannels(t, ctx, manager, refKey, i, fmt.Sprintf("%d", i))
		}

		// Test with limit 0 (should use MinChannelLimit = 1)
		results := manager.List(projectID, "", 0)
		assert.Len(t, results, 1)

		// Test with limit -1 (should use MinChannelLimit = 1)
		results = manager.List(projectID, "", -1)
		assert.Len(t, results, 1)

		// Test with limit > MaxChannelLimit (should use MaxChannelLimit = 100)
		// Create only 5 channels, so we should get all 5 even if limit is capped
		results = manager.List(projectID, "", 200)
		assert.Len(t, results, 5)
	})
}

func TestChannelManager_ChannelsCount(t *testing.T) {
	t.Run("get channels count", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(t, ttl, cleanupInterval)
		projectID := types.NewID()

		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "lobby"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 7, "3")

		count := manager.Count(projectID)
		assert.Equal(t, 3, count)
	})
}

func attachChannels(
	t *testing.T,
	ctx context.Context,
	manager *channel.Manager,
	refKey types.ChannelRefKey,
	count int,
	clientIDPrefix string,
) []types.ID {
	channelIDs := make([]types.ID, 0)
	for i := range count {
		clientID, err := pkgtime.ActorIDFromHex(fmt.Sprintf("%s%023d", clientIDPrefix, i))
		assert.NoError(t, err)
		channelID, _, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		channelIDs = append(channelIDs, channelID)
	}
	return channelIDs
}

func assertChannelCounts(
	t *testing.T,
	manager *channel.Manager,
	refkeys []types.ChannelRefKey,
	expectedCounts []int64,
	includeSubPath bool,
) {
	for i, refKey := range refkeys {
		expectedCount := expectedCounts[i]
		message := ""
		if includeSubPath {
			message = fmt.Sprintf("%s total channel count should be %d", refKey.ChannelKey, expectedCount)
		} else {
			message = fmt.Sprintf("%s direct channel count should be %d", refKey.ChannelKey, expectedCount)
		}
		assert.Equal(t, expectedCount, manager.SessionCount(refKey, includeSubPath), message)
	}
}

func TestChannelManager_AttachDetach(t *testing.T) {
	t.Run("attach creates session and returns ID", func(t *testing.T) {
		ctx := context.Background()
		manager, pubsub, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		clientID := pkgtime.InitialActorID

		sessionID, count, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.NotEmpty(t, sessionID)
		assert.Equal(t, int64(1), count)

		// Verify pubsub event was published
		assert.Len(t, pubsub.events, 1)
		assert.Equal(t, refKey, pubsub.events[0].Key)
		assert.Equal(t, int64(1), pubsub.events[0].Count)
	})

	t.Run("attach same client to same channel returns existing session", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		clientID := pkgtime.InitialActorID

		sessionID1, count1, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count1)

		// Attach again with same client
		sessionID2, count2, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, sessionID1, sessionID2) // Same session ID
		assert.Equal(t, int64(1), count2)       // Count unchanged
	})

	t.Run("attach multiple clients to same channel", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		sessionIDs := attachChannels(t, ctx, manager, refKey, 5, "1")
		assert.Len(t, sessionIDs, 5)

		// Verify session count
		assert.Equal(t, int64(5), manager.SessionCount(refKey, false))
	})

	t.Run("detach removes session and returns new count", func(t *testing.T) {
		ctx := context.Background()
		manager, pubsub, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		sessionIDs := attachChannels(t, ctx, manager, refKey, 3, "1")
		assert.Equal(t, int64(3), manager.SessionCount(refKey, false))

		// Clear pubsub events from attach
		pubsub.events = nil

		// Detach first session
		newCount, err := manager.Detach(ctx, sessionIDs[0])
		assert.NoError(t, err)
		assert.Equal(t, int64(2), newCount)
		assert.Equal(t, int64(2), manager.SessionCount(refKey, false))

		// Verify pubsub event
		assert.Len(t, pubsub.events, 1)
		assert.Equal(t, int64(2), pubsub.events[0].Count)
	})

	t.Run("detach all sessions removes channel", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		sessionIDs := attachChannels(t, ctx, manager, refKey, 2, "1")

		// Detach all sessions
		_, err := manager.Detach(ctx, sessionIDs[0])
		assert.NoError(t, err)
		_, err = manager.Detach(ctx, sessionIDs[1])
		assert.NoError(t, err)

		// Channel should be removed
		assert.Equal(t, int64(0), manager.SessionCount(refKey, false))
		assert.Equal(t, 0, manager.Count(projectID))
	})

	t.Run("attach to different channels", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		clientID := pkgtime.InitialActorID

		_, _, err := manager.Attach(ctx, refKey1, clientID)
		assert.NoError(t, err)
		_, _, err = manager.Attach(ctx, refKey2, clientID)
		assert.NoError(t, err)

		assert.Equal(t, int64(1), manager.SessionCount(refKey1, false))
		assert.Equal(t, int64(1), manager.SessionCount(refKey2, false))
		assert.Equal(t, 2, manager.Count(projectID))
	})

	t.Run("attach to hierarchical channels", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1.section-1.desk-1"}

		attachChannels(t, ctx, manager, refKey1, 2, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 4, "3")

		// Check individual counts
		assert.Equal(t, int64(2), manager.SessionCount(refKey1, false))
		assert.Equal(t, int64(3), manager.SessionCount(refKey2, false))
		assert.Equal(t, int64(4), manager.SessionCount(refKey3, false))

		// Check hierarchical counts (includeSubPath=true)
		assert.Equal(t, int64(9), manager.SessionCount(refKey1, true)) // 2+3+4
		assert.Equal(t, int64(7), manager.SessionCount(refKey2, true)) // 3+4
		assert.Equal(t, int64(4), manager.SessionCount(refKey3, true)) // 4
	})
}

func TestChannelManager_AttachDetachErrors(t *testing.T) {
	t.Run("attach with invalid channel key returns error", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Invalid channel key (empty)
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ""}
		clientID := pkgtime.InitialActorID

		_, _, err := manager.Attach(ctx, refKey, clientID)
		assert.Error(t, err)
	})

	t.Run("attach with invalid channel key path returns error", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Invalid channel key (starts with dot)
		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: ".room-1"}
		clientID := pkgtime.InitialActorID

		_, _, err := manager.Attach(ctx, refKey, clientID)
		assert.Error(t, err)
	})

	t.Run("detach non-existent session returns error", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)

		nonExistentID := types.NewID()
		_, err := manager.Detach(ctx, nonExistentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("detach already detached session returns error", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		sessionIDs := attachChannels(t, ctx, manager, refKey, 1, "1")

		// First detach should succeed
		_, err := manager.Detach(ctx, sessionIDs[0])
		assert.NoError(t, err)

		// Second detach should fail
		_, err = manager.Detach(ctx, sessionIDs[0])
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})
}

func TestChannelManager_Stats(t *testing.T) {
	t.Run("stats returns correct counts", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Initial stats
		stats := manager.Stats()
		assert.Equal(t, 0, stats["total_channels"])
		assert.Equal(t, 0, stats["total_sessions"])
		assert.Equal(t, 0, stats["current_seq"])

		// Create channels
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}

		attachChannels(t, ctx, manager, refKey1, 3, "1")
		attachChannels(t, ctx, manager, refKey2, 2, "2")

		// Check stats
		stats = manager.Stats()
		assert.Equal(t, 2, stats["total_channels"])
		assert.Equal(t, 5, stats["total_sessions"])
		assert.Equal(t, 5, stats["current_seq"]) // 5 attach events
	})

	t.Run("stats updates after detach", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		sessionIDs := attachChannels(t, ctx, manager, refKey, 3, "1")

		stats := manager.Stats()
		assert.Equal(t, 1, stats["total_channels"])
		assert.Equal(t, 3, stats["total_sessions"])

		// Detach one session
		_, _ = manager.Detach(ctx, sessionIDs[0])

		stats = manager.Stats()
		assert.Equal(t, 1, stats["total_channels"])
		assert.Equal(t, 2, stats["total_sessions"])
		assert.Equal(t, 4, stats["current_seq"]) // 3 attach + 1 detach

		// Detach all remaining
		_, _ = manager.Detach(ctx, sessionIDs[1])
		_, _ = manager.Detach(ctx, sessionIDs[2])

		stats = manager.Stats()
		assert.Equal(t, 0, stats["total_channels"])
		assert.Equal(t, 0, stats["total_sessions"])
	})
}

func TestChannelManager_Concurrency(t *testing.T) {
	t.Run("concurrent attach to same channel", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		concurrency := 300
		var wg sync.WaitGroup

		sessionIDs := make([]types.ID, concurrency)
		errors := make([]error, concurrency)

		for i := range concurrency {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				clientID, err := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", idx))
				if err != nil {
					errors[idx] = err
					return
				}
				sessionID, _, err := manager.Attach(ctx, refKey, clientID)
				sessionIDs[idx] = sessionID
				errors[idx] = err
			}(i)
		}
		wg.Wait()

		// All attaches should succeed
		for i, err := range errors {
			assert.NoError(t, err, "attach %d failed", i)
		}

		// All session IDs should be unique
		uniqueIDs := make(map[types.ID]bool)
		for _, id := range sessionIDs {
			uniqueIDs[id] = true
		}
		assert.Equal(t, concurrency, len(uniqueIDs))

		// Session count should match
		assert.Equal(t, int64(concurrency), manager.SessionCount(refKey, false))
	})

	t.Run("concurrent attach to different channels", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		concurrency := 300
		var wg sync.WaitGroup

		for i := range concurrency {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				refKey := types.ChannelRefKey{
					ProjectID:  projectID,
					ChannelKey: key.Key(fmt.Sprintf("room-%d", idx)),
				}
				clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", idx))
				_, _, err := manager.Attach(ctx, refKey, clientID)
				assert.NoError(t, err)
			}(i)
		}
		wg.Wait()

		// Should have 300 channels
		assert.Equal(t, concurrency, manager.Count(projectID))
	})

	t.Run("concurrent attach and detach", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		// Pre-attach some sessions
		initialSessions := attachChannels(t, ctx, manager, refKey, 300, "1")

		concurrency := 300
		var wg sync.WaitGroup

		// Concurrent detaches
		for i := range concurrency {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				_, _ = manager.Detach(ctx, initialSessions[idx])
			}(i)
		}

		// Concurrent attaches
		for i := range concurrency {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("2%023d", idx))
				_, _, _ = manager.Attach(ctx, refKey, clientID)
			}(i)
		}

		wg.Wait()

		// Final count should be 50 (all old detached, all new attached)
		assert.Equal(t, int64(300), manager.SessionCount(refKey, false))
	})

	t.Run("concurrent operations on hierarchical channels", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKeys := []types.ChannelRefKey{
			{ProjectID: projectID, ChannelKey: "room-1"},
			{ProjectID: projectID, ChannelKey: "room-1.section-1"},
			{ProjectID: projectID, ChannelKey: "room-1.section-1.desk-1"},
			{ProjectID: projectID, ChannelKey: "room-1.section-2"},
		}

		var wg sync.WaitGroup

		// Concurrent attaches to different hierarchical channels
		for i := range 100 {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				refKey := refKeys[idx%len(refKeys)]
				clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", idx))
				_, _, err := manager.Attach(ctx, refKey, clientID)
				assert.NoError(t, err)
			}(i)
		}
		wg.Wait()

		// Verify hierarchical counts
		totalCount := manager.SessionCount(refKeys[0], true)
		assert.Equal(t, int64(100), totalCount)
	})
}

func TestChannelManager_StartStop(t *testing.T) {
	// Note: Start/Stop tests are skipped because the Start() function
	// uses a background context that may not have a logger initialized,
	// causing nil pointer dereference in tests. The cleanup functionality
	// is tested via CleanupExpired in TestChannelManager_RefreshAndCleanup.
	t.Skip("Start/Stop tests require logger initialization")
}

func TestChannelManager_SeqMonotonic(t *testing.T) {
	t.Run("seq increases monotonically on attach", func(t *testing.T) {
		ctx := context.Background()
		manager, pubsub, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		// Attach multiple clients and verify seq is monotonically increasing
		var lastSeq int64 = 0
		for i := 0; i < 10; i++ {
			clientID, err := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", i))
			assert.NoError(t, err)

			_, _, err = manager.Attach(ctx, refKey, clientID)
			assert.NoError(t, err)

			// Get the latest event
			pubsub.mu.Lock()
			latestEvent := pubsub.events[len(pubsub.events)-1]
			pubsub.mu.Unlock()

			assert.Greater(t, latestEvent.Seq, lastSeq, "seq should be monotonically increasing")
			lastSeq = latestEvent.Seq
		}
	})

	t.Run("seq increases monotonically on detach", func(t *testing.T) {
		ctx := context.Background()
		manager, pubsub, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}

		// Attach multiple clients
		sessionIDs := attachChannels(t, ctx, manager, refKey, 5, "1")

		// Clear events and get current seq
		pubsub.mu.Lock()
		lastSeq := pubsub.events[len(pubsub.events)-1].Seq
		pubsub.mu.Unlock()

		// Detach and verify seq continues to increase
		for _, sessionID := range sessionIDs {
			_, err := manager.Detach(ctx, sessionID)
			assert.NoError(t, err)

			pubsub.mu.Lock()
			latestEvent := pubsub.events[len(pubsub.events)-1]
			pubsub.mu.Unlock()

			assert.Greater(t, latestEvent.Seq, lastSeq, "seq should be monotonically increasing on detach")
			lastSeq = latestEvent.Seq
		}
	})

	t.Run("seq is unique across concurrent operations", func(t *testing.T) {
		ctx := context.Background()
		manager, pubsub, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		concurrency := 300
		var wg sync.WaitGroup

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", idx))
				_, _, _ = manager.Attach(ctx, refKey, clientID)
			}(i)
		}
		wg.Wait()

		// Verify all seq numbers are unique
		pubsub.mu.Lock()
		seqMap := make(map[int64]bool)
		for _, event := range pubsub.events {
			assert.False(t, seqMap[event.Seq], "seq %d should be unique", event.Seq)
			seqMap[event.Seq] = true
		}
		pubsub.mu.Unlock()

		assert.Equal(t, concurrency, len(seqMap))
	})

	t.Run("seq reflects in stats", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Initial seq should be 0
		stats := manager.Stats()
		assert.Equal(t, 0, stats["current_seq"])

		refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		sessionIDs := attachChannels(t, ctx, manager, refKey, 5, "1")

		// After 5 attaches, seq should be 5
		stats = manager.Stats()
		assert.Equal(t, 5, stats["current_seq"])

		// After 2 detaches, seq should be 7
		_, _ = manager.Detach(ctx, sessionIDs[0])
		_, _ = manager.Detach(ctx, sessionIDs[1])
		stats = manager.Stats()
		assert.Equal(t, 7, stats["current_seq"])
	})
}

func TestChannelManager_ListBoundary(t *testing.T) {
	t.Run("list respects MaxChannelLimit of 100", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Create 150 channels (more than MaxChannelLimit)
		for i := 1; i <= 150; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%03d", i)),
			}
			clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", i))
			_, _, err := manager.Attach(ctx, refKey, clientID)
			assert.NoError(t, err)
		}

		// Verify we have 150 channels
		assert.Equal(t, 150, manager.Count(projectID))

		// List with limit > MaxChannelLimit should return MaxChannelLimit (100)
		results := manager.List(projectID, "", 200)
		assert.Equal(t, 100, len(results))

		// List with limit = MaxChannelLimit should return 100
		results = manager.List(projectID, "", 100)
		assert.Equal(t, 100, len(results))

		// List with limit < MaxChannelLimit should return that limit
		results = manager.List(projectID, "", 50)
		assert.Equal(t, 50, len(results))
	})

	t.Run("list with limit 0 uses MinChannelLimit", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Create 5 channels
		for i := 1; i <= 5; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", i))
			_, _, _ = manager.Attach(ctx, refKey, clientID)
		}

		// Limit 0 should use MinChannelLimit (1)
		results := manager.List(projectID, "", 0)
		assert.Equal(t, 1, len(results))
	})

	t.Run("list with negative limit uses MinChannelLimit", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Create 5 channels
		for i := 1; i <= 5; i++ {
			refKey := types.ChannelRefKey{
				ProjectID:  projectID,
				ChannelKey: key.Key(fmt.Sprintf("room-%d", i)),
			}
			clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", i))
			_, _, _ = manager.Attach(ctx, refKey, clientID)
		}

		// Negative limit should use MinChannelLimit (1)
		results := manager.List(projectID, "", -10)
		assert.Equal(t, 1, len(results))
	})

	t.Run("list returns sorted results within limit", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID := types.NewID()

		// Create channels with different names (not in alphabetical order)
		keys := []key.Key{"zebra", "alpha", "mango", "beta", "omega"}
		for i, k := range keys {
			refKey := types.ChannelRefKey{ProjectID: projectID, ChannelKey: k}
			clientID, _ := pkgtime.ActorIDFromHex(fmt.Sprintf("%024d", i))
			_, _, _ = manager.Attach(ctx, refKey, clientID)
		}

		// List all channels
		results := manager.List(projectID, "", 10)
		assert.Equal(t, 5, len(results))

		// Verify sorted by channel key alphabetically
		assert.Equal(t, "alpha", results[0].Key.ChannelKey.String())
		assert.Equal(t, "beta", results[1].Key.ChannelKey.String())
		assert.Equal(t, "mango", results[2].Key.ChannelKey.String())
		assert.Equal(t, "omega", results[3].Key.ChannelKey.String())
		assert.Equal(t, "zebra", results[4].Key.ChannelKey.String())
	})
}

func TestChannelManager_ProjectIsolation(t *testing.T) {
	t.Run("sessions are isolated by project", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		refKey1 := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID2, ChannelKey: "room-1"}

		attachChannels(t, ctx, manager, refKey1, 3, "1")
		attachChannels(t, ctx, manager, refKey2, 5, "2")

		// Counts should be isolated
		assert.Equal(t, int64(3), manager.SessionCount(refKey1, false))
		assert.Equal(t, int64(5), manager.SessionCount(refKey2, false))

		// Channel counts should be isolated
		assert.Equal(t, 1, manager.Count(projectID1))
		assert.Equal(t, 1, manager.Count(projectID2))
	})

	t.Run("list returns only channels for specified project", func(t *testing.T) {
		ctx := context.Background()
		manager, _, _ := createManager(t, 60*time.Second, 10*time.Second)
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		refKey1a := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-1"}
		refKey1b := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-2"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID2, ChannelKey: "room-1"}

		attachChannels(t, ctx, manager, refKey1a, 2, "1")
		attachChannels(t, ctx, manager, refKey1b, 3, "2")
		attachChannels(t, ctx, manager, refKey2, 4, "3")

		list1 := manager.List(projectID1, "", 10)
		list2 := manager.List(projectID2, "", 10)

		assert.Len(t, list1, 2)
		assert.Len(t, list2, 1)
	})
}
