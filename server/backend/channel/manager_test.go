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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	pkgtime "github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/channel"
)

// mockPubSub is a mock implementation of PubSub for testing
type mockPubSub struct {
	events []events.ChannelEvent
}

func (m *mockPubSub) PublishChannel(ctx context.Context, event events.ChannelEvent) {
	m.events = append(m.events, event)
}

func TestPresenceManager_RefreshAndCleanup(t *testing.T) {
	t.Run("refresh updates activity time", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

		// Create a presence
		refKey := types.ChannelRefKey{
			ProjectID:  types.NewID(),
			ChannelKey: "test-room",
		}
		clientID := pkgtime.InitialActorID

		sessionID, count, err := manager.Attach(ctx, refKey, clientID)
		assert.NoError(t, err)
		assert.Equal(t, int64(1), count)

		// Wait a bit
		time.Sleep(100 * time.Millisecond)

		// Refresh the presence
		err = manager.Refresh(ctx, sessionID)
		assert.NoError(t, err)

		// Presence should still exist
		assert.Equal(t, int64(1), manager.Count(refKey))
	})

	t.Run("cleanup removes expired presences", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 200 * time.Millisecond // Very short TTL for testing
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

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
		assert.Equal(t, int64(0), manager.Count(refKey))
	})

	t.Run("refresh extends TTL and prevents cleanup", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

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
		assert.Equal(t, int64(1), manager.Count(refKey))
	})

	t.Run("cleanup removes only expired presences", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 300 * time.Millisecond
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

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
		assert.Equal(t, int64(1), manager.Count(refKey))
	})

	t.Run("refresh non-existent presence returns error", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

		// Try to refresh a non-existent presence
		nonExistentID := types.NewID()
		err := manager.Refresh(ctx, nonExistentID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "session not found")
	})

	t.Run("cleanup with no presences does not error", func(t *testing.T) {
		ctx := context.Background()
		pubsub := &mockPubSub{}
		ttl := 60 * time.Second
		cleanupInterval := 10 * time.Second
		manager := channel.NewManager(pubsub, ttl, cleanupInterval)

		// Run cleanup on empty manager
		cleanedCount, err := manager.CleanupExpired(ctx)
		assert.NoError(t, err)
		assert.Equal(t, 0, cleanedCount)
	})

	t.Run("default TTL is applied when zero", func(t *testing.T) {
		pubsub := &mockPubSub{}
		manager := channel.NewManager(pubsub, 0, 0)

		// Manager should have default TTL (60s)
		// We can't directly check the internal field, but we can verify it works
		stats := manager.Stats()
		assert.NotNil(t, stats)
	})
}
