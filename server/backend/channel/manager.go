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

// Package channel provides channel management for real-time user tracking.
package channel

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ChannelKeyPathSeparator is the separator for channel key paths.
	ChannelKeyPathSeparator = "."

	// ErrSessionNotFound is returned when a session is not found.
	ErrSessionNotFound = errors.NotFound("session not found").WithCode("ErrSessionNotFound")

	// ErrInvalidChannelKey is returned when a channel key is invalid.
	ErrInvalidChannelKey = errors.InvalidArgument("channel key is invalid").WithCode("ErrInvalidChannelKey")
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

	// pubsub is used to publish channel events
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

// Attach adds a client to a channel and returns the unique session ID.
func (m *Manager) Attach(
	ctx context.Context,
	key types.ChannelRefKey,
	clientID time.ActorID,
) (types.ID, int64, error) {
	if !IsValidChannelKeyPath(key.ChannelKey) {
		return types.ID(""), 0, fmt.Errorf("attach %s: %w", key, ErrInvalidChannelKey)
	}

	// Check if client is already attached to this channel
	if sessionMap, ok := m.clientToSession.Get(clientID); ok {
		if sessionID, found := sessionMap.Get(key); found {
			return sessionID, m.PresenceCount(key, false), nil
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

// Detach removes a client from a channel using session ID.
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

// PresenceCount returns the current presence count for a channel key.
// This is a lock-free operation by directly querying the session map length.
// If includeSubPath is true, it returns the total count of sessions in the channel and all its sub-channels.
func (m *Manager) PresenceCount(key types.ChannelRefKey, includeSubPath bool) int64 {
	if !IsValidChannelKeyPath(key.ChannelKey) {
		return 0
	}

	if !includeSubPath {
		if sessionMap, ok := m.channels.Get(key); ok {
			return int64(sessionMap.Len())
		}
		return 0
	}

	totalCount := 0
	targetKeyPaths := ParseKeyPath(key.ChannelKey)
	for _, channelKey := range m.channels.Keys() {
		channelKeyPaths := ParseKeyPath(channelKey.ChannelKey)

		if !isSubKeyPath(channelKeyPaths, targetKeyPaths) {
			continue
		}

		if sessionMap, ok := m.channels.Get(channelKey); ok {
			totalCount += sessionMap.Len()
		}
	}
	return int64(totalCount)
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

// Stats returns statistics about the channel manager.
func (m *Manager) Stats() map[string]int {
	totalSessions := 0
	for _, sessionMap := range m.channels.Values() {
		totalSessions += sessionMap.Len()
	}

	return map[string]int{
		"total_presences": m.channels.Len(),
		"total_sessions":  totalSessions,
		"current_seq":     int(m.seqCounter.Load()),
	}
}

// FirstKeyPath returns the first key path of the given channel key.
func FirstKeyPath(key key.Key) string {
	if !IsValidChannelKeyPath(key) {
		return ""
	}

	return ParseKeyPath(key)[0]
}

// MergeKeyPath merges the given key paths into a single key path.
func MergeKeyPath(keyPaths []string) string {
	if len(keyPaths) == 0 {
		return ""
	}

	return strings.Join(keyPaths, ChannelKeyPathSeparator)
}

// ParseKeyPath splits a channel key into key path components.
func ParseKeyPath(key key.Key) []string {
	return strings.Split(key.String(), ChannelKeyPathSeparator)
}

// IsValidChannelKeyPath checks if a channel key is valid.
func IsValidChannelKeyPath(key key.Key) bool {
	if strings.HasPrefix(key.String(), ChannelKeyPathSeparator) || strings.HasSuffix(key.String(), ChannelKeyPathSeparator) {
		return false
	}

	return len(ParseKeyPath(key)) > 0
}

// isSubKeyPath checks if channelKeyPaths is a sub path of keyPaths.
func isSubKeyPath(channelKeyPaths, keyPaths []string) bool {
	if len(keyPaths) > len(channelKeyPaths) {
		return false
	}

	for i, keyPath := range keyPaths {
		if keyPath != channelKeyPaths[i] {
			return false
		}
	}

	return true
}
