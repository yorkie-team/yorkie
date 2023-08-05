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
	"encoding/json"
	"fmt"
	"sync"
)

// Map is a map of Presence. It is wrapper of sync.Map to use type-safe.
type Map struct {
	presences sync.Map
}

// NewMap creates a new instance of Map.
func NewMap() *Map {
	return &Map{presences: sync.Map{}}
}

// Store stores the given presence to the map.
func (m *Map) Store(clientID string, presence Presence) {
	m.presences.Store(clientID, presence)
}

// Range calls f sequentially for each key and value present in the map.
func (m *Map) Range(f func(clientID string, presence Presence) bool) {
	m.presences.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		presence := value.(Presence)
		return f(clientID, presence)
	})
}

// Load returns the presence for the given clientID.
func (m *Map) Load(clientID string) Presence {
	presence, ok := m.presences.Load(clientID)
	if !ok {
		return nil
	}

	return presence.(Presence)
}

// LoadOrStore returns the existing presence if exists.
// Otherwise, it stores and returns the given presence.
func (m *Map) LoadOrStore(clientID string, presence Presence) Presence {
	actual, _ := m.presences.LoadOrStore(clientID, presence)
	return actual.(Presence)
}

// Has returns whether the given clientID exists.
func (m *Map) Has(clientID string) bool {
	_, ok := m.presences.Load(clientID)
	return ok
}

// Delete deletes the presence for the given clientID.
func (m *Map) Delete(clientID string) {
	m.presences.Delete(clientID)
}

// DeepCopy copies itself deeply.
func (m *Map) DeepCopy() *Map {
	copied := NewMap()
	m.Range(func(clientID string, presence Presence) bool {
		copied.Store(clientID, presence.DeepCopy())
		return true
	})
	return copied
}

// PresenceChangeType represents the type of presence change.
type PresenceChangeType string

const (
	// Put represents the presence is put.
	Put PresenceChangeType = "put"

	// Clear represents the presence is cleared.
	Clear PresenceChangeType = "clear"
)

// PresenceChange represents the change of presence.
type PresenceChange struct {
	ChangeType PresenceChangeType `json:"changeType"`
	Presence   Presence           `json:"presence"`
}

// NewChangeFromJSON creates a new instance of PresenceChange from JSON.
func NewChangeFromJSON(encodedChange string) (*PresenceChange, error) {
	if encodedChange == "" {
		return nil, nil
	}

	p := &PresenceChange{}
	if err := json.Unmarshal([]byte(encodedChange), p); err != nil {
		return nil, fmt.Errorf("unmarshal presence change: %w", err)
	}

	return p, nil
}

// Presence represents custom presence that can be defined by the client.
type Presence map[string]string

// NewPresence creates a new instance of Presence.
func NewPresence() Presence {
	return make(map[string]string)
}

// Set sets the value of the given key.
func (p Presence) Set(key string, value string) {
	p[key] = value
}

// Clear clears the presence.
func (p Presence) Clear() {
	for k := range p {
		delete(p, k)
	}
}

// DeepCopy copies itself deeply.
func (p Presence) DeepCopy() Presence {
	if p == nil {
		return nil
	}
	clone := make(map[string]string)
	for k, v := range p {
		clone[k] = v
	}
	return clone
}
