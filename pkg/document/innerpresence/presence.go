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

// Map is a map of presence by clientID.
type Map = sync.Map // map[string]*innerpresence.Presence

// NewMap creates a new instance of Map.
func NewMap() *Map {
	return &sync.Map{}
}

// PresenceChangeType represents the type of presence change.
type PresenceChangeType string

const (
	// Put represents the presence is put.
	Put PresenceChangeType = "put"
)

// PresenceChange represents the change of presence.
type PresenceChange struct {
	ChangeType PresenceChangeType
	Presence   Presence
}

// NewChangeFromJSON creates a new instance of PresenceChange from JSON.
func NewChangeFromJSON(encodedJSON string) (*PresenceChange, error) {
	if encodedJSON == "" {
		return nil, nil
	}

	p := PresenceChange{}
	if err := json.Unmarshal([]byte(encodedJSON), &p); err != nil {
		return nil, fmt.Errorf("unmarshal presence change: %w", err)
	}

	return &p, nil
}

// Presence represents custom presence that can be defined by the client.
type Presence map[string]string

// NewPresence creates a new instance of Presence.
func NewPresence() *Presence {
	data := make(map[string]string)
	p := Presence(data)
	return &p
}

// Set sets the value of the given key.
func (p *Presence) Set(key string, value string) {
	presence := *p
	presence[key] = value
}
