/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package innerpresence provides the implementation of Presence.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
package innerpresence

import (
	"sync"
	"sync/atomic"
)

// Map is a map of Presence with mutl-routine safety.
type Map struct {
	mu        sync.RWMutex
	presences map[string]Presence
	copied    atomic.Bool
}

// NewMap creates a new instance of Map.
func NewMap() *Map {
	return &Map{
		presences: make(map[string]Presence),
	}
}

// Store stores the given presence to the map.
func (m *Map) Store(clientID string, presence Presence) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.copied.Load() {
		newPresences := make(map[string]Presence, len(m.presences))
		for k, v := range m.presences {
			newPresences[k] = v
		}
		m.presences = newPresences
		m.copied.Store(true)
	}

	m.presences[clientID] = presence.DeepCopy()
}

// Range calls f sequentially for each key and value present in the map.
func (m *Map) Range(f func(clientID string, presence Presence) bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for clientID, presence := range m.presences {
		if !f(clientID, presence) {
			break
		}
	}
}

// Load returns the presence for the given clientID.
func (m *Map) Load(clientID string) Presence {
	m.mu.RLock()
	defer m.mu.RUnlock()

	presence, ok := m.presences[clientID]
	if !ok {
		return nil
	}

	return presence
}

// LoadOrStore returns the existing presence if exists.
// Otherwise, it stores and returns the given presence.
func (m *Map) LoadOrStore(clientID string, presence Presence) Presence {
	m.mu.Lock()
	defer m.mu.Unlock()

	actual, ok := m.presences[clientID]
	if ok {
		return actual
	}

	if !m.copied.Load() {
		newPresences := make(map[string]Presence, len(m.presences))
		for k, v := range m.presences {
			newPresences[k] = v
		}
		m.presences = newPresences
		m.copied.Store(true)
	}

	clone := presence.DeepCopy()
	m.presences[clientID] = clone
	return clone
}

// Has returns whether the given clientID exists.
func (m *Map) Has(clientID string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.presences[clientID]
	return ok
}

// Delete deletes the presence for the given clientID.
func (m *Map) Delete(clientID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.copied.Load() {
		newPresences := make(map[string]Presence, len(m.presences))
		for k, v := range m.presences {
			if k != clientID {
				newPresences[k] = v
			}
		}
		m.presences = newPresences
		m.copied.Store(true)
	} else {
		delete(m.presences, clientID)
	}
}

// DeepCopy copies itself deeply.
func (m *Map) DeepCopy() *Map {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copied := &Map{
		presences: m.presences,
	}
	m.copied.Store(false)
	copied.copied.Store(false)
	return copied
}

// Presence represents custom presence that can be defined by the client.
type Presence map[string]string

// New creates a new instance of Presence.
func New() Presence {
	return make(map[string]string)
}

// Set sets the value of the given key.
func (p Presence) Set(key string, value string) {
	p[key] = value
}

// Clear clears the presence.
func (p *Presence) Clear() {
	*p = make(map[string]string)
}

// DeepCopy copies itself deeply.
func (p Presence) DeepCopy() Presence {
	if p == nil {
		return nil
	}

	clone := make(map[string]string, len(p))
	for k, v := range p {
		clone[k] = v
	}
	return clone
}
