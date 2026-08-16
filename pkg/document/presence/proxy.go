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

const (
	// Put and Clear are aliases for the corresponding constants in the inner package.
	Put   = inner.Put
	Clear = inner.Clear
)

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
	// Recorded before the swap, for the same reason Clear records: what undo
	// restores for a key is the value it held when the Update began, and
	// after the swap that value is no longer readable. The keys the incoming
	// data introduces are recorded too -- as absent, which is what they were.
	for key := range p.data {
		p.recordPrevious(key)
	}
	for key := range data {
		p.recordPrevious(key)
	}

	p.data = data
	if p.data == nil {
		p.data = NewData()
	}

	p.context.SetPresenceChange(Change{
		ChangeType: Put,
		Presence:   p.data,
	})
}

// SetOption configures a presence Set call.
type SetOption func(*setConfig)

// setConfig holds the options accumulated from a Set call's SetOptions.
type setConfig struct {
	addToHistory bool
}

// WithHistory marks the key set by this call as undoable, so the previous
// value is recorded on the change.Context and later restored when the
// document is undone.
func WithHistory() SetOption {
	return func(c *setConfig) {
		c.addToHistory = true
	}
}

// Set sets the value of the given key. By default the change is not
// undoable; pass WithHistory() to push the previous value onto the undo
// stack alongside any operation reverses from the same Update call.
func (p *Presence) Set(key string, value string, opts ...SetOption) {
	var cfg setConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	data := p.data
	p.recordPrevious(key)
	data.Set(key, value)

	p.context.SetPresenceChange(Change{
		ChangeType: Put,
		Presence:   data,
	})

	p.context.SetReversePresenceKey(key, cfg.addToHistory)
}

// Delete removes the given key from the presence. It is how Go spells JS's
// `presence.set({key: undefined})` (presence.ts:35-47): JS assigns undefined
// and its JSON-based deep copy drops the key before the Put reaches the
// wire. Go's presence data is a map of strings with no undefined to assign,
// so the key is removed outright, which is the same observable result.
//
// Like Set, the change is not undoable by default; pass WithHistory() to
// record the key so undoing this change restores whatever value it held.
func (p *Presence) Delete(key string, opts ...SetOption) {
	var cfg setConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	data := p.data
	p.recordPrevious(key)
	data.Remove(key)

	p.context.SetPresenceChange(Change{
		ChangeType: Put,
		Presence:   data,
	})

	p.context.SetReversePresenceKey(key, cfg.addToHistory)
}

// Clear clears the value of the given key.
func (p *Presence) Clear() {
	data := p.data

	// Every key this wipes is recorded first, for the same reason Set records
	// the key it overwrites: a Set marked WithHistory *after* this call has
	// to undo to the value its key held when the Update began, not to the
	// absence left behind here. JS reaches the same result by rebinding its
	// proxy to a fresh object and leaving its up-front snapshot untouched
	// (presence.ts:57-64).
	for key := range data {
		p.recordPrevious(key)
	}

	data.Clear()

	p.context.SetPresenceChange(Change{
		ChangeType: Clear,
	})
}

// recordPrevious reports the value the given key holds right now to the
// change context, which keeps only the first report per key -- the value as
// of the moment the context was created, since the proxy is the only writer
// of the presence for as long as the context is open.
func (p *Presence) recordPrevious(key string) {
	value, exists := p.data[key]
	p.context.RecordPreviousPresence(key, value, exists)
}
