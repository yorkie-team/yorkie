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
)

// Map is a map of Presence with mutl-routine safety.
type Map struct {
	mu        sync.RWMutex
	presences map[string]Presence
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
	m.presences[clientID] = presence
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
	if !ok {
		m.presences[clientID] = presence
		return presence
	}
	return actual
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

	delete(m.presences, clientID)
}

// DeepCopy copies itself deeply.
func (m *Map) DeepCopy() *Map {
	m.mu.RLock()
	defer m.mu.RUnlock()

	copied := &Map{
		presences: make(map[string]Presence, len(m.presences)),
	}
	for clientID, presence := range m.presences {
		copied.presences[clientID] = presence.DeepCopy()
	}
	return copied
}

// Presence represents custom presence that can be defined by the client.
type Presence []string

// New creates a new instance of Presence.
func New() Presence {
	return make(Presence, 0)
}

const NotFound = ""

// Get gets the value of the given key
func (p Presence) Get(key string) string {
	for i := 0; i < len(p); i += 2 {
		if p[i] == key {
			return p[i+1]
		}
	}
	// TODO(raara).
	return NotFound
}

// GetIndex TODO(raara).
func (p Presence) GetIndex(key string) int {
	for i := 0; i < len(p); i += 2 {
		if p[i] == key {
			return i
		}
	}
	return -1
}

// Set sets the value of the given key.
func (p *Presence) Set(key, value string) {
	if idx := p.GetIndex(key); idx != -1 {
		(*p)[idx] = key
		(*p)[idx+1] = value
		return
	}
	*p = append(*p, key)
	*p = append(*p, value)
}

// Clear clears the presence.
func (p *Presence) Clear() {
	*p = []string{}
}

// DeepCopy copies itself deeply.
func (p Presence) DeepCopy() Presence {
	if p == nil {
		return nil
	}

	clone := make(Presence, len(p))
	copy(clone, p)
	//clone = append(clone[:0], p...)

	return clone
}
