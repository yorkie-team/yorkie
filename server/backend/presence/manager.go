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
	"sync"
	"sync/atomic"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// PresenceInfo represents information about a presence session.
type PresenceInfo struct {
	ClientID time.ActorID
	Key      key.Key
}

// CountUpdate represents a presence count change event.
type CountUpdate struct {
	Key   key.Key
	Count int64
	Seq   int64 // Monotonic sequence number for ordering
}

// Manager manages presence counters for real-time user tracking.
// It provides approximate counting with fast read/subscribe operations.
type Manager struct {
	// presences maps presence key to a map of client sessions
	presences map[key.Key]map[time.ActorID]*PresenceInfo

	// counts caches the current count for each presence key
	counts map[key.Key]int64

	// subscribers maps presence key to subscriber channels
	subscribers map[key.Key]map[string]chan CountUpdate

	// mu protects all maps
	mu sync.RWMutex

	// seqCounter is a monotonic counter for ordering events
	seqCounter atomic.Int64
}

// NewManager creates a new presence manager.
func NewManager() *Manager {
	return &Manager{
		presences:   make(map[key.Key]map[time.ActorID]*PresenceInfo),
		counts:      make(map[key.Key]int64),
		subscribers: make(map[key.Key]map[string]chan CountUpdate),
	}
}

// nextSeq returns the next monotonic sequence number.
func (m *Manager) nextSeq() int64 {
	return m.seqCounter.Add(1)
}

// Attach adds a client to a presence counter.
func (m *Manager) Attach(ctx context.Context, clientID time.ActorID, presenceKey key.Key) (string, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize presence map if not exists
	if m.presences[presenceKey] == nil {
		m.presences[presenceKey] = make(map[time.ActorID]*PresenceInfo)
	}

	// Check if client is already attached
	if _, exists := m.presences[presenceKey][clientID]; exists {
		// Client already attached, return current count
		return fmt.Sprintf("%s:%s", presenceKey, clientID), m.counts[presenceKey], nil
	}

	// Add new presence session
	m.presences[presenceKey][clientID] = &PresenceInfo{
		ClientID: clientID,
		Key:      presenceKey,
	}

	// Update count
	m.counts[presenceKey]++
	newCount := m.counts[presenceKey]

	// Notify subscribers
	m.notifySubscribers(presenceKey, newCount)

	presenceID := fmt.Sprintf("%s:%s", presenceKey, clientID)
	return presenceID, newCount, nil
}

// Detach removes a client from a presence counter.
func (m *Manager) Detach(ctx context.Context, clientID time.ActorID, presenceKey key.Key) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Check if presence exists
	if m.presences[presenceKey] == nil {
		return 0, nil
	}

	// Check if client is attached
	if _, exists := m.presences[presenceKey][clientID]; !exists {
		return m.counts[presenceKey], nil
	}

	// Remove client
	delete(m.presences[presenceKey], clientID)

	// Update count
	if m.counts[presenceKey] > 0 {
		m.counts[presenceKey]--
	}
	newCount := m.counts[presenceKey]

	// Clean up empty presence map
	if len(m.presences[presenceKey]) == 0 {
		delete(m.presences, presenceKey)
		delete(m.counts, presenceKey)
		newCount = 0
	}

	// Notify subscribers
	m.notifySubscribers(presenceKey, newCount)

	return newCount, nil
}

// GetCount returns the current count for a presence key.
func (m *Manager) GetCount(presenceKey key.Key) int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return m.counts[presenceKey]
}

// Subscribe creates a subscription for count updates on a presence key.
func (m *Manager) Subscribe(presenceKey key.Key, subscriberID string) <-chan CountUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Initialize subscribers map if not exists
	if m.subscribers[presenceKey] == nil {
		m.subscribers[presenceKey] = make(map[string]chan CountUpdate)
	}

	// Get current count while holding the lock
	currentCount := m.counts[presenceKey]

	// Create subscriber channel and send initial count immediately
	ch := make(chan CountUpdate, 10)

	// Send initial count synchronously to ensure it's available immediately
	ch <- CountUpdate{
		Key:   presenceKey,
		Count: currentCount, // Use the count captured while holding lock
		Seq:   0,            // Zero seq indicates initial state
	}

	m.subscribers[presenceKey][subscriberID] = ch

	return ch
}

// Unsubscribe removes a subscription.
func (m *Manager) Unsubscribe(presenceKey key.Key, subscriberID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.subscribers[presenceKey] == nil {
		return
	}

	if ch, exists := m.subscribers[presenceKey][subscriberID]; exists {
		close(ch)
		delete(m.subscribers[presenceKey], subscriberID)

		// Clean up empty subscribers map
		if len(m.subscribers[presenceKey]) == 0 {
			delete(m.subscribers, presenceKey)
		}
	}
}

// notifySubscribers sends count updates to all subscribers (must be called with lock held).
func (m *Manager) notifySubscribers(presenceKey key.Key, count int64) {
	if m.subscribers[presenceKey] == nil {
		return
	}

	update := CountUpdate{
		Key:   presenceKey,
		Count: count,
		Seq:   m.nextSeq(),
	}

	for subscriberID, ch := range m.subscribers[presenceKey] {
		select {
		case ch <- update:
		default:
			// Channel is full, skip this subscriber
			// In production, you might want to log this
			_ = subscriberID
		}
	}
}

// GetStats returns statistics about the presence manager.
func (m *Manager) GetStats() map[string]interface{} {
	m.mu.RLock()
	defer m.mu.RUnlock()

	totalPresences := len(m.presences)
	totalClients := 0
	totalSubscribers := 0

	for _, clients := range m.presences {
		totalClients += len(clients)
	}

	for _, subs := range m.subscribers {
		totalSubscribers += len(subs)
	}

	return map[string]interface{}{
		"total_presences":   totalPresences,
		"total_clients":     totalClients,
		"total_subscribers": totalSubscribers,
		"current_seq":       m.seqCounter.Load(),
	}
}
