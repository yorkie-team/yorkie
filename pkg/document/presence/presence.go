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

// Package presence provides the implementation of InternalPresence.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
package presence

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Map is a map of presence by clientID.
type Map = sync.Map // map[string]*presence.InternalPresence

// NewMap creates a new instance of Map.
func NewMap() *Map {
	return &sync.Map{}
}

// InternalPresence represents custom presence that can be defined by the client.
type InternalPresence map[string]string

// NewFromJSON creates a new instance of InternalPresence from JSON.
func NewFromJSON(encodedJSON string) (*InternalPresence, error) {
	if encodedJSON == "" {
		return nil, nil
	}

	p := InternalPresence{}
	if err := json.Unmarshal([]byte(encodedJSON), &p); err != nil {
		return nil, fmt.Errorf("unmarshal presence: %w", err)
	}

	return &p, nil
}

// NewInternalPresence creates a new instance of InternalPresence.
func NewInternalPresence() *InternalPresence {
	data := make(map[string]string)
	p := InternalPresence(data)
	return &p
}

// Set sets the value of the given key.
func (p *InternalPresence) Set(key string, value string) {
	presence := *p
	presence[key] = value
}
