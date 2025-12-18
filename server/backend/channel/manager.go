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
	"strings"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/channel"
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
	ErrInvalidChannelKey = errors.InvalidArgument("channel key is invalid").WithCode("ErrInvalidChannelKey")

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

// ChannelInfo represents information about a channel.
type ChannelInfo struct {
	Key      types.ChannelRefKey
	Sessions int
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

// NewManager creates a new presence manager.
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
		channels:        cmap.New[types.ChannelRefKey, *cmap.Map[types.ID, *Session]](),
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

// Attach adds a client to a channel and returns the unique session ID.
func (m *Manager) Attach(
	ctx context.Context,
	key types.ChannelRefKey,
	clientID time.ActorID,
) (types.ID, int64, error) {
	if !channel.IsValidChannelKeyPath(key.ChannelKey) {
		return types.ID(""), 0, channel.ErrInvalidChannelKey
	}

	// Check if client is already attached to this channel
	if sessionMap, ok := m.clientToSession.Get(clientID); ok {
		if sessionID, found := sessionMap.Get(key); found {
			return sessionID, m.SessionCount(key, false), nil
		}
	}

	sessionMap := m.channels.Upsert(
		key,
		func(val *cmap.Map[types.ID, *Session], exists bool) *cmap.Map[types.ID, *Session] {
			if !exists {
				val = cmap.New[types.ID, *Session]()

				if err := m.broker.Produce(ctx, messaging.ChannelEventsMessage{
					ProjectID:  key.ProjectID.String(),
					EventType:  events.ChannelCreated,
					Timestamp:  gotime.Now(),
					ChannelKey: key.ChannelKey.String(),
				}); err != nil {
					logging.From(ctx).Errorf("failed to produce channel event: %v", err)
				}
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

// SessionCount returns the current session count for a channel key.
// This is a lock-free operation by directly querying the session map length.
// If includeSubPath is true, it returns the total count of sessions in the channel and all its sub-channels.
func (m *Manager) SessionCount(key types.ChannelRefKey, includeSubPath bool) int64 {
	if !channel.IsValidChannelKeyPath(key.ChannelKey) {
		return 0
	}

	if !includeSubPath {
		if sessionMap, ok := m.channels.Get(key); ok {
			return int64(sessionMap.Len())
		}
		return 0
	}

	totalCount := 0
	targetKeyPaths, err := channel.ParseKeyPath(key.ChannelKey)
	if err != nil {
		return 0
	}
	for _, channelKey := range m.channels.Keys() {
		channelKeyPaths, err := channel.ParseKeyPath(channelKey.ChannelKey)
		if err != nil {
			continue
		}

		if !isKeyPathPrefix(channelKeyPaths, targetKeyPaths) {
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

	for _, key := range m.channels.Keys() {
		sessionMap, ok := m.channels.Get(key)
		if !ok {
			continue
		}

		// Check if session has expired (UpdatedAt + TTL < now)
		expiredSessionIDs := classifyExpiredSessions(now, m.sessionTTL, sessionMap)

		// Remove expired sessions
		cleanedCount += cleanUpExpiredSessions(ctx, m, expiredSessionIDs)
	}

	// Collect and publish metrics after cleanup
	if m.metrics != nil && m.metrics.Metrics != nil {
		m.collectAndPublishMetrics(ctx)
	}

	return cleanedCount, nil
}

// collectAndPublishMetrics collects channel and session metrics and publishes them to Prometheus.
func (m *Manager) collectAndPublishMetrics(ctx context.Context) {
	metrics := newChannelMetrics()

	for _, key := range m.channels.Keys() {
		sessionMap, ok := m.channels.Get(key)
		if !ok {
			continue
		}

		sessionCount := sessionMap.Len()
		if sessionCount == 0 {
			continue
		}

		project, err := m.db.FindProjectInfoByID(ctx, key.ProjectID)
		if err != nil {
			logging.From(ctx).Warnf("collect metrics: find project info %s: %v", key.ProjectID, err)
			continue
		}

		metrics.record(project.ToProject(), key, sessionCount)
	}

	metrics.publish(m.metrics)
}

// Stats returns statistics about the channel manager.
func (m *Manager) Stats() map[string]int {
	totalSessions := 0
	for _, sessionMap := range m.channels.Values() {
		totalSessions += sessionMap.Len()
	}

	return map[string]int{
		"total_channels": m.channels.Len(),
		"total_sessions": totalSessions,
		"current_seq":    int(m.seqCounter.Load()),
	}
}

// ListChannels lists channels for the given project ID.
// If query is not empty, it filters channels by the query prefix.
// It returns up to limit channels.
func (m *Manager) ListChannels(
	projectID types.ID,
	query string,
	limit int,
) []ChannelInfo {
	if limit <= 0 {
		limit = MinChannelLimit
	}
	if limit > MaxChannelLimit {
		limit = MaxChannelLimit
	}

	results := make([]ChannelInfo, 0)
	for _, key := range m.channels.Keys() {
		if key.ProjectID != projectID {
			continue
		}

		if query != "" && !strings.HasPrefix(key.ChannelKey.String(), query) {
			continue
		}

		sessionMap, ok := m.channels.Get(key)
		if !ok {
			continue
		}

		sessionCount := sessionMap.Len()
		if sessionCount > 0 {
			results = append(results, ChannelInfo{
				Key:      key,
				Sessions: sessionCount,
			})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Key.ChannelKey.String() < results[j].Key.ChannelKey.String()
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return results
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

// isKeyPathPrefix checks if baseKeyPaths is a prefix of targetKeyPaths.
func isKeyPathPrefix(baseKeyPaths, targetKeyPaths []string) bool {
	if len(baseKeyPaths) < len(targetKeyPaths) {
		return false
	}

	for i, keyPath := range targetKeyPaths {
		if keyPath != baseKeyPaths[i] {
			return false
		}
	}

	return true
}
