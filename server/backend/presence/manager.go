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

// Package presence provides presence management for real-time user tracking.
package presence

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// PubSub is an interface for publishing presence events.
type PubSub interface {
	PublishPresence(ctx context.Context, event events.PresenceEvent)
}

// PresenceInfo represents information about a presence session.
type PresenceInfo struct {
	ID       types.ID             // Unique presence session ID
	ClientID time.ActorID         // Client who created this presence
	RefKey   types.PresenceRefKey // Reference to the presence counter
}

// Manager manages presence counters for real-time user tracking.
type Manager struct {
	// presences maps PresenceRefKey to a map of unique presence IDs.
	presences *cmap.Map[types.PresenceRefKey, *cmap.Map[types.ID, *PresenceInfo]]

	// clientToPresence maps clientID to their presence IDs for quick lookup.
	clientToPresence *cmap.Map[time.ActorID, *cmap.Map[types.PresenceRefKey, types.ID]]

	// presenceIDToRefKey is a reverse index for O(1) Detach lookup
	presenceIDToRefKey *cmap.Map[types.ID, types.PresenceRefKey]

	// pubsub is used to publish presence events
	pubsub PubSub

	// seqCounter is a monotonic counter for ordering events
	seqCounter atomic.Int64
}

// NewManager creates a new presence manager.
func NewManager(pubsub PubSub) *Manager {
	return &Manager{
		presences:          cmap.New[types.PresenceRefKey, *cmap.Map[types.ID, *PresenceInfo]](),
		clientToPresence:   cmap.New[time.ActorID, *cmap.Map[types.PresenceRefKey, types.ID]](),
		presenceIDToRefKey: cmap.New[types.ID, types.PresenceRefKey](),
		pubsub:             pubsub,
	}
}

// nextSeq returns the next monotonic sequence number.
func (m *Manager) nextSeq() int64 {
	return m.seqCounter.Add(1)
}

// Attach adds a client to a presence counter and returns the unique presence ID.
func (m *Manager) Attach(
	ctx context.Context,
	refKey types.PresenceRefKey,
	clientID time.ActorID,
) (types.ID, int64, error) {
	// Check if client is already attached to this presence
	if clientPresenceMap, exists := m.clientToPresence.Get(clientID); exists {
		if presenceID, found := clientPresenceMap.Get(refKey); found {
			// Client already attached, return existing presence ID and current count
			return presenceID, m.Count(refKey), nil
		}
	}

	// Generate unique presence ID
	presenceID := types.NewID()

	// Get or create presence map for this refKey
	presenceMap := m.presences.Upsert(
		refKey,
		func(val *cmap.Map[types.ID, *PresenceInfo], exists bool) *cmap.Map[types.ID, *PresenceInfo] {
			if !exists {
				val = cmap.New[types.ID, *PresenceInfo]()
			}
			return val
		},
	)

	// Add new presence session
	presenceMap.Set(presenceID, &PresenceInfo{
		ID:       presenceID,
		ClientID: clientID,
		RefKey:   refKey,
	})

	// Add reverse index for O(1) detach lookup
	m.presenceIDToRefKey.Set(presenceID, refKey)

	// Get or create client presence map
	clientPresenceMap := m.clientToPresence.Upsert(
		clientID,
		func(val *cmap.Map[types.PresenceRefKey, types.ID], exists bool) *cmap.Map[types.PresenceRefKey, types.ID] {
			if !exists {
				val = cmap.New[types.PresenceRefKey, types.ID]()
			}
			return val
		},
	)
	clientPresenceMap.Set(refKey, presenceID)

	// Get new count immediately after adding to minimize race window
	newCount := int64(presenceMap.Len())

	// Publish event to PubSub
	m.pubsub.PublishPresence(ctx, events.PresenceEvent{
		RefKey: refKey,
		Count:  newCount,
		Seq:    m.nextSeq(),
	})

	return presenceID, newCount, nil
}

// Detach removes a client from a presence counter using presence ID.
func (m *Manager) Detach(
	ctx context.Context,
	presenceID types.ID,
) (int64, error) {
	// Use reverse index for O(1) lookup instead of O(n) iteration
	refKey, exists := m.presenceIDToRefKey.Get(presenceID)
	if !exists {
		return 0, fmt.Errorf("presence not found: %s", presenceID)
	}

	// Get presence map
	presenceMap, exists := m.presences.Get(refKey)
	if !exists {
		return 0, fmt.Errorf("presence map not found for key: %s", refKey)
	}

	// Get presence info
	info, exists := presenceMap.Get(presenceID)
	if !exists {
		return 0, fmt.Errorf("presence info not found: %s", presenceID)
	}

	// Remove from presences
	presenceMap.Delete(presenceID)

	// Get new count immediately after removal to minimize race window
	newCount := int64(presenceMap.Len())

	// Remove from reverse index
	m.presenceIDToRefKey.Delete(presenceID)

	// Remove from client mapping
	if clientPresenceMap, exists := m.clientToPresence.Get(info.ClientID); exists {
		clientPresenceMap.Delete(refKey)

		// Clean up empty client map
		if clientPresenceMap.Len() == 0 {
			m.clientToPresence.Delete(info.ClientID)
		}
	}

	// Clean up empty presence map if count reaches 0
	if newCount == 0 {
		m.presences.Delete(refKey)
	}

	// Publish event to PubSub
	m.pubsub.PublishPresence(ctx, events.PresenceEvent{
		RefKey: refKey,
		Count:  newCount,
		Seq:    m.nextSeq(),
	})

	return newCount, nil
}

// Count returns the current count for a presence key.
// This is a lock-free operation by directly querying the presence map length.
func (m *Manager) Count(refKey types.PresenceRefKey) int64 {
	if presenceMap, ok := m.presences.Get(refKey); ok {
		return int64(presenceMap.Len())
	}
	return 0
}

// Stats returns statistics about the presence manager.
func (m *Manager) Stats() map[string]int {
	totalPresences := m.presences.Len()
	totalSessions := 0

	// Count all sessions across all presence maps
	for _, presenceMap := range m.presences.Values() {
		totalSessions += presenceMap.Len()
	}

	return map[string]int{
		"total_presences": totalPresences,
		"total_sessions":  totalSessions,
		"current_seq":     int(m.seqCounter.Load()),
	}
}
