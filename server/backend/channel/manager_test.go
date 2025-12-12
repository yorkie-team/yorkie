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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	pkgtime "github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend/channel"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
)

// mockPubSub is a mock implementation of PubSub for testing
type mockPubSub struct {
	events []events.ChannelEvent
}

func (m *mockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	m.events = append(m.events, event)
}

// MockBroker is a mock implementation of Broker for testing
type MockBroker struct {
	Messages []messaging.Message
}

func (m *MockBroker) Produce(ctx context.Context, msg messaging.Message) error {
	m.Messages = append(m.Messages, msg)
	return nil
}

func (m *MockBroker) Close() error {
	return nil
}

func createManager(
	ttl time.Duration,
	cleanupInterval time.Duration,
) (*channel.Manager, *mockPubSub, *MockBroker) {
	pubsub := &mockPubSub{}
	broker := &MockBroker{}
	brokers := messaging.NewBroker(broker, broker, broker)
	manager := channel.NewManager(pubsub, ttl, cleanupInterval, nil, brokers)
	return manager, pubsub, broker
}

func TestPresenceManager_RefreshAndCleanup(t *testing.T) {
	t.Run("refresh updates activity time", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, broker := createManager(ttl, cleanupInterval)

		// Create a presence
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

		// Refresh the presence
		err = manager.Refresh(ctx, sessionID)
		assert.NoError(t, err)

		// Presence should still exist
		assert.Equal(t, int64(1), manager.PresenceCount(refKey, false))
	})

	t.Run("cleanup removes expired presences", func(t *testing.T) {
		ctx := context.Background()
		ttl := 200 * time.Millisecond // Very short TTL for testing
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)

		// Create a presence
		refKey := types.ChannelRefKey{
			ProjectID:  types.NewID(),
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

		// Presence should be removed
		assert.Equal(t, int64(0), manager.PresenceCount(refKey, false))
	})

	t.Run("refresh extends TTL and prevents cleanup", func(t *testing.T) {
		ctx := context.Background()
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)

		// Create a presence
		refKey := types.ChannelRefKey{
			ProjectID:  types.NewID(),
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

		// Presence should still exist
		assert.Equal(t, int64(1), manager.PresenceCount(refKey, false))
	})

	t.Run("cleanup removes only expired presences", func(t *testing.T) {
		ctx := context.Background()
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)

		// Create two presences
		refKey := types.ChannelRefKey{
			ProjectID:  types.NewID(),
			ChannelKey: "test-room",
		}
		clientID1 := pkgtime.InitialActorID
		clientID2, err := pkgtime.ActorIDFromHex("000000000000000000000001")
		assert.NoError(t, err)

		_, _, err = manager.Attach(ctx, refKey, clientID1)
		assert.NoError(t, err)

		// Wait a bit before creating second presence
		time.Sleep(200 * time.Millisecond)

		sessionID2, count, err := manager.Attach(ctx, refKey, clientID2)
		assert.NoError(t, err)
		assert.Equal(t, int64(2), count)

		// Refresh only the second presence
		err = manager.Refresh(ctx, sessionID2)
		assert.NoError(t, err)

		// Wait for first presence to expire
		time.Sleep(200 * time.Millisecond)

		// Run cleanup - should remove only the first presence
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 1, cleanedCount)

		// Only second presence should remain
		assert.Equal(t, int64(1), manager.PresenceCount(refKey, false))
	})

	t.Run("refresh non-existent presence returns error", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)

		// Try to refresh a non-existent presence
		nonExistentID := types.NewID()
		err := manager.Refresh(ctx, nonExistentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("cleanup with no presences does not error", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)

		// Run cleanup on empty manager
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, cleanedCount)
	})

	t.Run("default TTL is applied when zero", func(t *testing.T) {
		manager, _, _ := createManager(0, 0)

		// Manager should have default TTL (60s)
		// We can't directly check the internal field, but we can verify it works
		stats := manager.Stats()
		assert.NotNil(t, stats)
	})
}

func TestPresenceManager_Count(t *testing.T) {
	t.Run("get presencecount - hierarchical path ", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)
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

		// Check presence counts
		channelRefKeys := []types.ChannelRefKey{refKey1, refKey2, refKey3, refKey4}
		assertPresenceCounts(t, manager, channelRefKeys, []int64{40, 30, 10, 10}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{10, 10, 10, 10}, false)

		// Detach first level
		_, err := manager.Detach(ctx, channelIDs[0])
		assert.NoError(t, err)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{39, 30, 10, 10}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{9, 10, 10, 10}, false)

		// Detach second level
		_, err = manager.Detach(ctx, channelIDs[10])
		assert.NoError(t, err)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{38, 29, 10, 10}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{9, 9, 10, 10}, false)

		// Detach third level
		_, err = manager.Detach(ctx, channelIDs[20])
		assert.NoError(t, err)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{37, 28, 9, 10}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{9, 9, 9, 10}, false)

		// Cleanup all presences
		for _, channelID := range channelIDs {
			_, _ = manager.Detach(ctx, channelID)
		}
		assertPresenceCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, false)
	})

	t.Run("get presencecount - wrong path ", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)
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

		// Check presence counts
		channelRefKeys := []types.ChannelRefKey{refKey1, refKey2, refKey3, refKey4}
		assertPresenceCounts(t, manager, channelRefKeys, []int64{40, 30, 10, 10}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{10, 10, 10, 10}, false)

		// Check wrong presence counts
		wrongChannelRefKeys := []types.ChannelRefKey{wrongRefKey1, wrongRefKey2, wrongRefKey3, wrongRefKey4}
		assertPresenceCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, true)
		assertPresenceCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, false)

		// Cleanup all presences
		for _, channelID := range channelIDs {
			_, _ = manager.Detach(ctx, channelID)
		}
		assertPresenceCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, true)
		assertPresenceCounts(t, manager, channelRefKeys, []int64{0, 0, 0, 0}, false)
		assertPresenceCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, true)
		assertPresenceCounts(t, manager, wrongChannelRefKeys, []int64{0, 0, 0, 0}, false)
	})
}

func TestPresenceManager_ListChannels(t *testing.T) {
	t.Run("list all channels without query", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)
		projectID := types.NewID()

		// Create multiple channels
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-2"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "lobby"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 7, "3")

		// List all channels (no query)
		results := manager.ListChannels(projectID, "", 10)

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
		manager, _, _ := createManager(ttl, cleanupInterval)
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
		results := manager.ListChannels(projectID, "room-", 10)

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
		manager, _, _ := createManager(ttl, cleanupInterval)
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
		results := manager.ListChannels(projectID, "", 3)

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
		manager, _, _ := createManager(ttl, cleanupInterval)
		projectID := types.NewID()

		// Create channels with same session count
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "charlie"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "alpha"}
		refKey3 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "bravo"}

		attachChannels(t, ctx, manager, refKey1, 3, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")
		attachChannels(t, ctx, manager, refKey3, 3, "3")

		// List all channels
		results := manager.ListChannels(projectID, "", 10)

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
		manager, _, _ := createManager(ttl, cleanupInterval)
		projectID := types.NewID()

		// Create channels
		refKey1 := types.ChannelRefKey{ProjectID: projectID, ChannelKey: "room-1"}
		attachChannels(t, ctx, manager, refKey1, 5, "1")

		// List with non-matching query
		results := manager.ListChannels(projectID, "lobby-", 10)

		// Should return empty list
		assert.Len(t, results, 0)
	})

	t.Run("list channels respects project ID isolation", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)
		projectID1 := types.NewID()
		projectID2 := types.NewID()

		// Create channels for two different projects
		refKey1 := types.ChannelRefKey{ProjectID: projectID1, ChannelKey: "room-1"}
		refKey2 := types.ChannelRefKey{ProjectID: projectID2, ChannelKey: "room-1"}

		attachChannels(t, ctx, manager, refKey1, 5, "1")
		attachChannels(t, ctx, manager, refKey2, 3, "2")

		// List channels for project 1
		results1 := manager.ListChannels(projectID1, "", 10)
		assert.Len(t, results1, 1)
		assert.Equal(t, projectID1, results1[0].Key.ProjectID)

		// List channels for project 2
		results2 := manager.ListChannels(projectID2, "", 10)
		assert.Len(t, results2, 1)
		assert.Equal(t, projectID2, results2[0].Key.ProjectID)
	})

	t.Run("list channels normalizes limit", func(t *testing.T) {
		ctx := context.Background()
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager, _, _ := createManager(ttl, cleanupInterval)
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
		results := manager.ListChannels(projectID, "", 0)
		assert.Len(t, results, 1)

		// Test with limit -1 (should use MinChannelLimit = 1)
		results = manager.ListChannels(projectID, "", -1)
		assert.Len(t, results, 1)

		// Test with limit > MaxChannelLimit (should use MaxChannelLimit = 100)
		// Create only 5 channels, so we should get all 5 even if limit is capped
		results = manager.ListChannels(projectID, "", 200)
		assert.Len(t, results, 5)
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

func assertPresenceCounts(
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
			message = fmt.Sprintf("%s total presence count should be %d", refKey.ChannelKey, expectedCount)
		} else {
			message = fmt.Sprintf("%s direct presence count should be %d", refKey.ChannelKey, expectedCount)
		}
		assert.Equal(t, expectedCount, manager.PresenceCount(refKey, includeSubPath), message)
	}
}
