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

// Package channel provides channel management implementation.
package channel

import (
	"context"
	"fmt"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrSessionNotFound is returned when a session is not found.
	ErrSessionNotFound = errors.NotFound("session not found").WithCode("ErrSessionNotFound")
)

// PubSub is an interface for publishing channel events.
type PubSub interface {
	PublishChannel(ctx context.Context, event events.ChannelEvent)
}

// Session represents a single session.
type Session struct {
	ID        types.ID            // Unique session ID
	Key       types.ChannelRefKey // Reference to the channel
	Actor     time.ActorID        // Client who created this session
	UpdatedAt gotime.Time         // Last activity time for TTL calculation
}

// Manager manages channel.
type Manager struct {
	// channels maps channel keys to their active sessions.
	channels *cmap.Map[types.ChannelRefKey, *cmap.Map[types.ID, *Session]]

	// clientToSession maps client IDs to their associated channel keys and session IDs.
	clientToSession *cmap.Map[time.ActorID, *cmap.Map[types.ChannelRefKey, types.ID]]

	// sessionIDToKey is a reverse index for O(1) Detach lookup
	sessionIDToKey *cmap.Map[types.ID, types.ChannelRefKey]

	// pubsub is used to publish presence events
	pubsub PubSub

	// seqCounter is a monotonic counter for ordering events
	seqCounter atomic.Int64

	// presenceTTL is the time-to-live duration for sessions
	presenceTTL gotime.Duration

	// cleanupInterval is the interval for running cleanup of expired presences
	cleanupInterval gotime.Duration

	// cleanupTicker is the ticker for periodic cleanup
	cleanupTicker *gotime.Ticker

	// cleanupDone is the channel to signal cleanup goroutine to stop
	cleanupDone chan struct{}
}

// NewManager creates a new presence manager.
func NewManager(pubsub PubSub, ttl gotime.Duration, cleanupInterval gotime.Duration) *Manager {
	if ttl == 0 {
		ttl = 60 * gotime.Second
	}

	if cleanupInterval == 0 {
		cleanupInterval = 10 * gotime.Second
	}

	return &Manager{
		channels:        cmap.New[types.ChannelRefKey, *cmap.Map[types.ID, *Session]](),
		clientToSession: cmap.New[time.ActorID, *cmap.Map[types.ChannelRefKey, types.ID]](),
		sessionIDToKey:  cmap.New[types.ID, types.ChannelRefKey](),

		pubsub: pubsub,

		presenceTTL:     ttl,
		cleanupInterval: cleanupInterval,
		cleanupDone:     make(chan struct{}),
	}
}

// Start begins the background cleanup process for expired presences.
func (m *Manager) Start() {
	m.cleanupTicker = gotime.NewTicker(m.cleanupInterval)

	go func() {
		ctx := context.Background()
		for {
			select {
			case <-m.cleanupTicker.C:
				if expired, err := m.CleanupExpired(ctx); err == nil && expired > 0 {
					stats := m.Stats()
					logging.From(ctx).Infof(
						"PRE : presences[%d], sessions[%d], expires[%d]",
						stats["total_presences"],
						stats["total_sessions"],
						expired,
					)
				}
			case <-m.cleanupDone:
				return
			}
		}
	}()
}

// Stop stops the background cleanup process.
func (m *Manager) Stop() {
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
	}

	close(m.cleanupDone)
}

// nextSeq returns the next monotonic sequence number.
func (m *Manager) nextSeq() int64 {
	return m.seqCounter.Add(1)
}

// Attach adds a client to a channel and returns the unique presence ID.
func (m *Manager) Attach(
	ctx context.Context,
	key types.ChannelRefKey,
	clientID time.ActorID,
) (types.ID, int64, error) {
	// Check if client is already attached to this presence
	if sessionMap, ok := m.clientToSession.Get(clientID); ok {
		if sessionID, found := sessionMap.Get(key); found {
			return sessionID, m.Count(key), nil
		}
	}

	sessionMap := m.channels.Upsert(
		key,
		func(val *cmap.Map[types.ID, *Session], exists bool) *cmap.Map[types.ID, *Session] {
			if !exists {
				val = cmap.New[types.ID, *Session]()
			}

			return val
		},
	)

	id := types.NewID()
	sessionMap.Set(id, &Session{
		ID:        id,
		Actor:     clientID,
		Key:       key,
		UpdatedAt: gotime.Now(),
	})

	// Add reverse index for O(1) detach lookup
	m.sessionIDToKey.Set(id, key)

	// Get or create client to session map
	clientSessionMap := m.clientToSession.Upsert(
		clientID,
		func(val *cmap.Map[types.ChannelRefKey, types.ID], exists bool) *cmap.Map[types.ChannelRefKey, types.ID] {
			if !exists {
				val = cmap.New[types.ChannelRefKey, types.ID]()
			}

			return val
		},
	)
	clientSessionMap.Set(key, id)

	// Get new count immediately after adding to minimize race window
	newCount := int64(sessionMap.Len())

	// Publish event to PubSub
	m.pubsub.PublishChannel(ctx, events.ChannelEvent{
		Key:   key,
		Count: newCount,
		Seq:   m.nextSeq(),
	})

	return id, newCount, nil
}

// Detach removes a client from a channel using presence ID.
func (m *Manager) Detach(
	ctx context.Context,
	id types.ID,
) (int64, error) {
	key, ok := m.sessionIDToKey.Get(id)
	if !ok {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	sessionMap, ok := m.channels.Get(key)
	if !ok {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	session, ok := sessionMap.Get(id)
	if !ok {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	sessionMap.Delete(id)

	newCount := int64(sessionMap.Len())

	m.sessionIDToKey.Delete(id)

	if clientSessionMap, ok := m.clientToSession.Get(session.Actor); ok {
		clientSessionMap.Delete(key)

		if clientSessionMap.Len() == 0 {
			m.clientToSession.Delete(session.Actor)
		}
	}

	if newCount == 0 {
		m.channels.Delete(key)
	}

	m.pubsub.PublishChannel(ctx, events.ChannelEvent{
		Key:   key,
		Count: newCount,
		Seq:   m.nextSeq(),
	})

	return newCount, nil
}

// Refresh extends the TTL of an existing session by updating its activity time.
func (m *Manager) Refresh(
	ctx context.Context,
	id types.ID,
) error {
	key, ok := m.sessionIDToKey.Get(id)
	if !ok {
		return fmt.Errorf("refresh %s: %w", id, ErrSessionNotFound)
	}

	sessionMap, ok := m.channels.Get(key)
	if !ok {
		return fmt.Errorf("refresh %s: %w", id, ErrSessionNotFound)
	}

	info, ok := sessionMap.Get(id)
	if !ok {
		return fmt.Errorf("refresh %s: %w", id, ErrSessionNotFound)
	}

	sessionMap.Set(id, &Session{
		ID:        info.ID,
		Key:       info.Key,
		Actor:     info.Actor,
		UpdatedAt: gotime.Now(),
	})

	return nil
}

// Count returns the current count for a presence key.
// This is a lock-free operation by directly querying the presence map length.
func (m *Manager) Count(key types.ChannelRefKey) int64 {
	if presenceMap, ok := m.channels.Get(key); ok {
		return int64(presenceMap.Len())
	}

	return 0
}

// CleanupExpired removes sessions that have exceeded their TTL.
// Returns the number of cleaned up sessions.
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	now := gotime.Now()
	cleanedCount := 0

	for _, sessionMap := range m.channels.Values() {
		expiredIDs := []types.ID{}

		// Check if session has expired (UpdatedAt + TTL < now)
		for _, session := range sessionMap.Values() {
			if now.Sub(session.UpdatedAt) > m.presenceTTL {
				expiredIDs = append(expiredIDs, session.ID)
			}
		}

		// Remove expired sessions
		for _, id := range expiredIDs {
			if _, err := m.Detach(ctx, id); err != nil {
				logging.From(ctx).Warnf("detach expired session of %s: %v", id, err)
				continue
			}

			cleanedCount++
		}
	}

	return cleanedCount, nil
}

// Stats returns statistics about the presence manager.
func (m *Manager) Stats() map[string]int {
	totalSessions := 0
	for _, presenceMap := range m.channels.Values() {
		totalSessions += presenceMap.Len()
	}

	return map[string]int{
		"total_presences": m.channels.Len(),
		"total_sessions":  totalSessions,
		"current_seq":     int(m.seqCounter.Load()),
	}
}
