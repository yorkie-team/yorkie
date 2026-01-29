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
	"sort"
	"sync"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	pkgchannel "github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrSessionNotFound is returned when a session is not found.
	ErrSessionNotFound = errors.NotFound("session not found").WithCode("ErrSessionNotFound")

	// ErrInvalidChannelKey is returned when a channel key is invalid.
	ErrInvalidChannelKey = pkgchannel.ErrInvalidChannelKey

	// MinChannelLimit is the minimum limit for listing channels.
	MinChannelLimit = 1

	// MaxChannelLimit is the maximum limit for listing channels.
	MaxChannelLimit = 100
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

// Channel represents a channel.
type Channel struct {
	Key      types.ChannelRefKey
	Sessions *cmap.Map[types.ID, *Session]
	mu       sync.Mutex // Serializes Attach/Detach to prevent race conditions
}

// ChannelSessionCountInfo represents information about a channel.
type ChannelSessionCountInfo struct {
	Key      types.ChannelRefKey
	Sessions int
}

// Manager manages channels and sessions for real-time user tracking.
type Manager struct {
	// channels is a lock-free hierarchical trie that maps channel keys to their active sessions.
	channels *ChannelTrie

	// clientToSession maps client IDs to their associated channel keys and session IDs.
	clientToSession *cmap.Map[time.ActorID, *cmap.Map[types.ChannelRefKey, types.ID]]

	// sessionIDToKey is a reverse index for O(1) Detach lookup
	sessionIDToKey *cmap.Map[types.ID, types.ChannelRefKey]

	// pubsub is used to publish channel events
	pubsub PubSub

	// seqCounter is a monotonic counter for ordering events
	seqCounter atomic.Int64

	// sessionTTL is the time-to-live duration for sessions
	sessionTTL gotime.Duration

	// cleanupInterval is the interval for running cleanup of expired presences
	cleanupInterval gotime.Duration

	// cleanupTicker is the ticker for periodic cleanup
	cleanupTicker *gotime.Ticker

	// cleanupDone is the channel to signal cleanup goroutine to stop
	cleanupDone chan struct{}

	// metrics is used to collect prometheus metrics
	metrics *Metrics

	// broker is used to publish channel events
	broker messaging.Broker

	// db is used to get project info
	db database.Database
}

// NewManager creates a new channel manager.
func NewManager(
	pubsub PubSub,
	ttl gotime.Duration,
	cleanupInterval gotime.Duration,
	metrics *Metrics,
	broker messaging.Broker,
	db database.Database,
) *Manager {
	if ttl == 0 {
		ttl = 60 * gotime.Second
	}

	if cleanupInterval == 0 {
		cleanupInterval = 10 * gotime.Second
	}

	return &Manager{
		channels:        NewChannelTrie(),
		clientToSession: cmap.New[time.ActorID, *cmap.Map[types.ChannelRefKey, types.ID]](),
		sessionIDToKey:  cmap.New[types.ID, types.ChannelRefKey](),

		pubsub: pubsub,

		sessionTTL:      ttl,
		cleanupInterval: cleanupInterval,
		cleanupDone:     make(chan struct{}),

		metrics: metrics,
		broker:  broker,
		db:      db,
	}
}

// Start begins the background cleanup process for expired sessions.
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
						"CHAN: channels[%d], sessions[%d], expires[%d]",
						stats["total_channels"],
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

func (m *Manager) upsertChannel(ctx context.Context, key types.ChannelRefKey) *Channel {
	// isNew tracks whether this goroutine created the channel.
	// GetOrInsert uses double-check locking internally:
	// - Fast path: if the key exists, returns immediately without calling create()
	// - Slow path: acquires lock, re-checks, and only calls create() if still missing
	// This ensures create() is called exactly once, so isNew is true for exactly one goroutine.
	isNew := false
	value := m.channels.GetOrInsert(key, func() *Channel {
		isNew = true
		return &Channel{
			Key:      key,
			Sessions: cmap.New[types.ID, *Session](),
		}
	})

	// Publish event only for the goroutine that actually created the channel
	if isNew && value != nil {
		if err := m.broker.Produce(ctx, messaging.ChannelEventsMessage{
			ProjectID:  key.ProjectID.String(),
			EventType:  events.ChannelCreated,
			Timestamp:  gotime.Now(),
			ChannelKey: key.ChannelKey.String(),
		}); err != nil {
			logging.From(ctx).Errorf("failed to produce channel event: %v", err)
		}
	}

	return value
}

// Attach adds a client to a channel and returns the unique session ID.
func (m *Manager) Attach(
	ctx context.Context,
	key types.ChannelRefKey,
	clientID time.ActorID,
) (types.ID, int64, error) {
	if !pkgchannel.IsValidChannelKeyPath(key.ChannelKey) {
		return types.ID(""), 0, ErrInvalidChannelKey
	}

	// Check if client is already attached to this channel
	if sessionMap, ok := m.clientToSession.Get(clientID); ok {
		if sessionID, found := sessionMap.Get(key); found {
			return sessionID, m.SessionCount(key, false), nil
		}
	}

	// Retry loop for double-check pattern: if the channel was deleted by a concurrent
	// Detach between GetOrInsert and acquiring the lock, we retry with a new channel.
	for {
		channel := m.upsertChannel(ctx, key)
		if channel == nil {
			return types.ID(""), 0, fmt.Errorf("create channel failed: invalid channel key %s", key.ChannelKey)
		}

		channel.mu.Lock()

		// Double-check: verify the channel is still in the trie.
		// A concurrent Detach may have deleted it after upsertChannel but before we acquired the lock.
		if current := m.channels.Get(key); current != channel {
			channel.mu.Unlock()
			continue // Retry with fresh channel
		}

		id := types.NewID()
		channel.Sessions.Set(id, &Session{
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

		newCount := int64(channel.Sessions.Len())

		channel.mu.Unlock()

		if err := m.broker.Produce(ctx, messaging.SessionEventsMessage{
			ProjectID:  key.ProjectID.String(),
			SessionID:  id.String(),
			Timestamp:  gotime.Now(),
			UserID:     clientID.String(),
			ChannelKey: key.ChannelKey.String(),
			EventType:  events.SessionCreated,
		}); err != nil {
			logging.From(ctx).Errorf("failed to produce session event: %v", err)
		}

		// Publish event to PubSub
		m.pubsub.PublishChannel(ctx, events.ChannelEvent{
			Key:   key,
			Count: newCount,
			Seq:   m.nextSeq(),
		})

		return id, newCount, nil
	}
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

	ch := m.channels.Get(key)
	if ch == nil {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	ch.mu.Lock()

	session, ok := ch.Sessions.Get(id)
	if !ok {
		ch.mu.Unlock()
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	ch.Sessions.Delete(id)

	newCount := int64(ch.Sessions.Len())

	m.sessionIDToKey.Delete(id)

	if clientSessionMap, ok := m.clientToSession.Get(session.Actor); ok {
		clientSessionMap.Delete(key)

		if clientSessionMap.Len() == 0 {
			m.clientToSession.Delete(session.Actor)
		}
	}

	// Delete channel while holding the lock to prevent race with Attach.
	// ChannelTrie.Delete also cleans up empty shards internally.
	if newCount == 0 {
		m.channels.Delete(key)
	}

	ch.mu.Unlock()

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

	ch := m.channels.Get(key)
	if ch == nil {
		return fmt.Errorf("refresh %s: %w", id, ErrSessionNotFound)
	}

	info, ok := ch.Sessions.Get(id)
	if !ok {
		return fmt.Errorf("refresh %s: %w", id, ErrSessionNotFound)
	}

	ch.Sessions.Set(id, &Session{
		ID:        info.ID,
		Key:       info.Key,
		Actor:     info.Actor,
		UpdatedAt: gotime.Now(),
	})

	return nil
}

// SessionCount returns the current session count for a channel key.
// This is a lock-free operation by directly querying the session map length.
// If includeSubPath is true, it returns the total count of sessions in the channel and all its sub-channels.
func (m *Manager) SessionCount(channelkey types.ChannelRefKey, includeSubPath bool) int64 {
	if !pkgchannel.IsValidChannelKeyPath(channelkey.ChannelKey) {
		return 0
	}

	if !includeSubPath {
		ch := m.channels.Get(channelkey)
		if ch != nil {
			return int64(ch.Sessions.Len())
		}
		return 0
	}

	totalCount := 0
	// Use ForEachDescendant to get all child channels in the hierarchy
	m.channels.ForEachDescendant(channelkey, func(ch *Channel) bool {
		totalCount += ch.Sessions.Len()
		return true
	})

	return int64(totalCount)
}

// CleanupExpired removes sessions that have exceeded their TTL.
// Returns the number of cleaned up sessions.
func (m *Manager) CleanupExpired(ctx context.Context) (int, error) {
	now := gotime.Now()
	cleanedCount := 0

	// Collect channel keys first - this is now lock-free with HierarchicalTrie
	channelKeys := make([]types.ChannelRefKey, 0)
	m.channels.ForEach(func(ch *Channel) bool {
		if ch != nil {
			channelKeys = append(channelKeys, ch.Key)
		}
		return true
	})

	// Process each channel with proper locking
	for _, key := range channelKeys {
		ch := m.channels.Get(key)
		if !m.isValidChannel(ch) {
			continue
		}

		// Check if session has expired (UpdatedAt + TTL < now)
		expiredSessionIDs := classifyExpiredSessions(now, m.sessionTTL, ch.Sessions)

		// Remove expired sessions
		cleanedCount += cleanUpExpiredSessions(ctx, m, expiredSessionIDs)
	}

	// Collect and publish metrics after cleanup
	if m.metrics != nil && m.metrics.Metrics != nil {
		m.collectAndPublishMetrics(ctx)
	}

	return cleanedCount, nil
}

func classifyExpiredSessions(
	now gotime.Time,
	sessionTTL gotime.Duration,
	sessionMap *cmap.Map[types.ID, *Session],
) []types.ID {
	expiredIDs := []types.ID{}
	for _, session := range sessionMap.Values() {
		if now.Sub(session.UpdatedAt) > sessionTTL {
			expiredIDs = append(expiredIDs, session.ID)
		}
	}

	return expiredIDs
}

func cleanUpExpiredSessions(ctx context.Context, manager *Manager, expiredSessionIDs []types.ID) int {
	cleanedCount := 0
	for _, id := range expiredSessionIDs {
		if _, err := manager.Detach(ctx, id); err != nil {
			logging.From(ctx).Warnf("detach expired session of %s: %v", id, err)
			continue
		}
		cleanedCount++
	}
	return cleanedCount
}

// collectAndPublishMetrics collects channel and session metrics and publishes them to Prometheus.
func (m *Manager) collectAndPublishMetrics(ctx context.Context) {
	metrics := newChannelMetrics()

	m.channels.ForEach(func(ch *Channel) bool {
		if !m.isValidChannel(ch) {
			return true
		}

		sessionCount := ch.Sessions.Len()
		if sessionCount == 0 {
			return true
		}

		key := ch.Key

		project, err := m.db.FindProjectInfoByID(ctx, key.ProjectID)
		if err != nil {
			logging.From(ctx).Warnf("collect metrics: find project info %s: %v", key.ProjectID, err)
			return true
		}

		metrics.record(project.ToProject(), key, sessionCount)
		return true
	})

	metrics.publish(m.metrics)
}

// Stats returns statistics about the channel manager.
func (m *Manager) Stats() map[string]int {
	totalChannels := 0
	totalSessions := 0
	m.channels.ForEach(func(ch *Channel) bool {
		if !m.isValidChannel(ch) {
			return true
		}
		totalChannels++
		totalSessions += ch.Sessions.Len()
		return true
	})

	return map[string]int{
		"total_channels": totalChannels,
		"total_sessions": totalSessions,
		"current_seq":    int(m.seqCounter.Load()),
	}
}

// List lists channels for the given project ID.
// If query is not empty, it filters channels by the query prefix.
// It returns up to limit channels.
func (m *Manager) List(
	projectID types.ID,
	query string,
	limit int,
) []ChannelSessionCountInfo {
	if limit <= 0 {
		limit = MinChannelLimit
	}
	if limit > MaxChannelLimit {
		limit = MaxChannelLimit
	}

	results := make([]ChannelSessionCountInfo, 0)
	if query != "" {
		// Query is a channel key prefix
		m.channels.ForEachPrefix(query, projectID, func(ch *Channel) bool {
			m.collectActiveChannelInfo(ch, &results)
			return true
		})
	} else {
		// No query: list all channels for this project
		m.channels.ForEachInProject(projectID, func(ch *Channel) bool {
			m.collectActiveChannelInfo(ch, &results)
			return true
		})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Key.ChannelKey.String() < results[j].Key.ChannelKey.String()
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
}

// Count returns the current channels count.
func (m *Manager) Count(projectID types.ID) int {
	count := 0
	m.channels.ForEachInProject(projectID, func(ch *Channel) bool {
		if ch != nil {
			count++
		}
		return true
	})
	return count
}

// collectActiveChannelInfo collects channel information if it has active sessions.
// Returns true if the channel was added to results, false otherwise.
func (m *Manager) collectActiveChannelInfo(ch *Channel, results *[]ChannelSessionCountInfo) bool {
	if !m.isValidChannel(ch) {
		return false
	}
	sessionCount := ch.Sessions.Len()
	if sessionCount > 0 {
		*results = append(*results, ChannelSessionCountInfo{
			Key:      ch.Key,
			Sessions: sessionCount,
		})
		return true
	}
	return false
}

// isValidChannel checks if a channel is valid (non-nil with initialized sessions).
func (m *Manager) isValidChannel(ch *Channel) bool {
	return ch != nil && ch.Sessions != nil
}
