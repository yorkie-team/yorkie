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
	ID    types.ID            // Unique session ID
	Key   types.ChannelRefKey // Reference to the channel
	Actor time.ActorID        // Client who created this session

	// updatedAt stores the last activity time as UnixNano (atomic).
	// Using atomic allows Refresh to update this field without acquiring
	// a cmap write lock — only a read lock is needed to get the Session
	// pointer, then the atomic store updates the timestamp lock-free.
	updatedAt atomic.Int64
}

// Channel represents a channel.
type Channel struct {
	Key      types.ChannelRefKey
	Sessions *cmap.Map[types.ID, *Session]

	// activeCount tracks the number of active sessions atomically.
	// It is incremented in Attach before adding to Sessions, and decremented
	// after removing from Sessions. This ensures Detach can check for zero
	// without holding a write lock, while Attach's increment prevents
	// premature channel deletion.
	activeCount atomic.Int64

	// mu serializes Detach's channel deletion (write lock) with Attach's
	// commit phase (read lock). Concurrent Attaches share the read lock and
	// do not contend with each other; only Detach's deletion blocks them.
	// Without this serialization, Attach's pre/post-check could observe the
	// channel in the trie before Detach's atomic store committed, while
	// Detach's activeCount load could observe zero before Attach's increment
	// became visible — both passing their checks for the same channel that
	// Detach then removes from the trie.
	mu sync.RWMutex
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

	// cleanupInterval is the interval for running cleanup of expired sessions
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
		if err := produceChannelEvent(ctx, m, key, events.ChannelCreated); err != nil {
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

	// Retry loop: if the channel was deleted by a concurrent Detach between
	// GetOrInsert and the activeCount increment, we retry with a new channel.
	// Attach acquires ch.mu only as a read lock for the commit phase, which
	// blocks only against Detach's write-locked deletion — concurrent Attaches
	// share the read lock without contending.
	for {
		channel := m.upsertChannel(ctx, key)
		if channel == nil {
			return types.ID(""), 0, fmt.Errorf("create channel failed: invalid channel key %s", key.ChannelKey)
		}

		// Increment activeCount BEFORE checking the trie. This is a fast-path
		// signal to Detach that an Attach is in flight; the read lock below is
		// what actually serializes against Detach's deletion.
		channel.activeCount.Add(1)

		// Pre-check: fast retry if the channel was already deleted before we
		// incremented. This avoids taking the read lock when retry is certain.
		if current := m.channels.Get(key); current != channel {
			channel.activeCount.Add(-1)
			continue
		}

		sessionID := types.NewID()
		session := &Session{
			ID:    sessionID,
			Actor: clientID,
			Key:   key,
		}
		session.updatedAt.Store(gotime.Now().UnixNano())

		// Acquire the read lock for the commit phase. This serializes with
		// Detach's write lock for deletion: either we acquire the read lock
		// before Detach acquires the write lock (so Detach's activeCount load
		// observes our increment and skips deletion), or we acquire after
		// Detach releases (so the re-check below sees the trie deletion and
		// we retry). Without this lock, Detach's load could observe zero
		// before our increment became visible while our trie check observed
		// the channel before Detach's atomic store committed — both passing
		// their checks for a channel that Detach then removes.
		channel.mu.RLock()
		if current := m.channels.Get(key); current != channel {
			channel.mu.RUnlock()
			channel.activeCount.Add(-1)
			continue
		}

		channel.Sessions.Set(sessionID, session)
		m.sessionIDToKey.Set(sessionID, key)

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
		clientSessionMap.Set(key, sessionID)

		channel.mu.RUnlock()

		newSessionCount := int64(channel.Sessions.Len())

		if err := produceSessionEvent(ctx, m, sessionID, clientID, key, events.SessionCreated); err != nil {
			logging.From(ctx).Errorf("failed to produce session event: %v", err)
		}

		// Publish event to PubSub
		m.pubsub.PublishChannel(ctx, events.ChannelEvent{
			Key:          key,
			SessionCount: newSessionCount,
			Seq:          m.nextSeq(),
		})

		return sessionID, newSessionCount, nil
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

	session, ok := ch.Sessions.Get(id)
	if !ok {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	// Use Sessions.Delete's return value to ensure exactly one goroutine
	// performs cleanup when concurrent Detach calls race for the same
	// session ID. Without this, both would decrement activeCount, driving
	// it below zero so the channel is never removed from m.channels.
	if !ch.Sessions.Delete(id) {
		return 0, fmt.Errorf("detach %s: %w", id, ErrSessionNotFound)
	}

	newSessionCount := int64(ch.Sessions.Len())

	m.sessionIDToKey.Delete(id)

	if clientSessionMap, ok := m.clientToSession.Get(session.Actor); ok {
		clientSessionMap.Delete(key)

		if clientSessionMap.Len() == 0 {
			m.clientToSession.Delete(session.Actor)
		}
	}

	// Decrement activeCount AFTER removing from Sessions and indexes.
	// Only acquire the write lock when count reaches zero (channel deletion).
	// The write lock excludes Attach's read-locked commit phase, ensuring that
	// either Attach observed the channel as still present and incremented
	// activeCount before we load it (so we skip deletion), or Attach observes
	// the trie deletion under the read lock and retries.
	if ch.activeCount.Add(-1) == 0 {
		ch.mu.Lock()
		if ch.activeCount.Load() == 0 {
			m.channels.Delete(key)
		}
		ch.mu.Unlock()
	}

	m.pubsub.PublishChannel(ctx, events.ChannelEvent{
		Key:          key,
		SessionCount: newSessionCount,
		Seq:          m.nextSeq(),
	})

	return newSessionCount, nil
}

// DetachByActor detaches channel sessions held by the given actor under the
// given project on this server. It is used during client deactivation to
// cascade cleanup without waiting for the channel session TTL to expire.
//
// Sessions are scoped to the project — a sibling project that happens to
// share the same actor ID is untouched. Errors on individual sessions are
// logged but do not abort the loop; the goal is to evict as much as
// possible, and the cleanup ticker catches anything missed.
func (m *Manager) DetachByActor(
	ctx context.Context,
	projectID types.ID,
	actor time.ActorID,
) (int, error) {
	clientSessionMap, ok := m.clientToSession.Get(actor)
	if !ok {
		return 0, nil
	}

	// Snapshot (channelRefKey, sessionID) pairs so we can filter by project
	// without keeping the inner cmap pinned for the duration of Detach calls.
	channelKeys := clientSessionMap.Keys()
	type sessionRef struct {
		key types.ChannelRefKey
		id  types.ID
	}
	targets := make([]sessionRef, 0, len(channelKeys))
	for _, key := range channelKeys {
		if key.ProjectID != projectID {
			continue
		}
		sessionID, ok := clientSessionMap.Get(key)
		if !ok {
			continue
		}
		targets = append(targets, sessionRef{key: key, id: sessionID})
	}

	detached := 0
	for _, t := range targets {
		if _, err := m.Detach(ctx, t.id); err != nil {
			logging.From(ctx).Warnf("detach session %s for actor %s: %v", t.id, actor, err)
			continue
		}
		detached++
	}

	return detached, nil
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

	info.updatedAt.Store(gotime.Now().UnixNano())

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
		updatedAt := gotime.Unix(0, session.updatedAt.Load())
		if now.Sub(updatedAt) > sessionTTL {
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

func produceChannelEvent(
	ctx context.Context,
	manager *Manager,
	key types.ChannelRefKey,
	eventType events.ChannelEventType,
) error {
	return manager.broker.Produce(ctx, messaging.ChannelEventsMessage{
		ProjectID:  key.ProjectID.String(),
		EventType:  eventType,
		Timestamp:  gotime.Now(),
		ChannelKey: key.ChannelKey.String(),
	})
}

func produceSessionEvent(
	ctx context.Context,
	manager *Manager,
	sessionID types.ID,
	clientID time.ActorID,
	key types.ChannelRefKey,
	eventType events.ChannelEventType,
) error {
	return manager.broker.Produce(ctx, messaging.SessionEventsMessage{
		ProjectID:  key.ProjectID.String(),
		SessionID:  sessionID.String(),
		Timestamp:  gotime.Now(),
		UserID:     clientID.String(),
		ChannelKey: key.ChannelKey.String(),
		EventType:  eventType,
	})
}
