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

// Package presence provides the implementation of Presence.
package presence

import (
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
)

// Data is an alias for the underlying presence data type used internally.
// Exporting this alias lets external packages consume presence data without
// importing the internal `inner` package directly.
type Data = inner.Presence

// Map is an alias for the internal presences map implementation. This is
// exported so higher-level packages can reference the map type when needed
// without depending on the inner package.
type Map = inner.Map

// Change is an alias for inner.Change so external packages (server, api)
// can reference presence changes without importing the internal package.
type Change = inner.Change

var Put = inner.Put
var Clear = inner.Clear

// NewData creates a new instance of Data.
func NewData() Data {
	return inner.New()
}

// NewMap creates a new instance of Map.
func NewMap() *Map {
	return inner.NewMap()
}

// Presence is a proxy for the inner.Presence to be manipulated from the outside.
type Presence struct {
	context *change.Context
	data    Data
}

// New creates a new instance of Presence.
func New(ctx *change.Context, data Data) *Presence {
	return &Presence{
		context: ctx,
		data:    data,
	}
}

// Initialize initializes the presence.
func (p *Presence) Initialize(data Data) {
	p.data = data
	if p.data == nil {
		p.data = NewData()
	}

	p.context.SetPresenceChange(Change{
		ChangeType: Put,
		Presence:   p.data,
	})
}

// Set sets the value of the given key.
func (p *Presence) Set(key string, value string) {
	data := p.data
	data.Set(key, value)

	p.context.SetPresenceChange(Change{
		ChangeType: Put,
		Presence:   data,
	})
}

// Clear clears the value of the given key.
func (p *Presence) Clear() {
	data := p.data
	data.Clear()

	p.context.SetPresenceChange(Change{
		ChangeType: Clear,
	})
}
