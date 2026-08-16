/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package change

import (
	"sort"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// prevPresence is the value a presence key held when a Context was created,
// recorded by the presence proxy the first time it mutates that key. exists
// distinguishes a key that held the empty string from one that held nothing:
// undo restores the former and removes the latter.
type prevPresence struct {
	key    string
	value  string
	exists bool
}

// Context is used to record the context of modification when editing a document.
// Each time we add an operation, a new time ticket is issued.
// Finally, returns a Change after the modification has been completed.
type Context struct {
	prevID         ID
	nextID         ID
	message        string
	operations     []operations.Operation
	delimiter      uint32
	root           *crdt.Root
	presenceChange *inner.Change

	// previousPresence holds, for each presence key this context has
	// mutated, the value that key held when the context was created.
	// ReversePresence rebuilds its return value from it for any key marked
	// undoable via presence.WithHistory.
	//
	// JS deep-copies the actor's whole presence up front (context.ts:69).
	// Recording it a key at a time, on the first mutation the presence proxy
	// makes to that key, yields the same values -- the proxy is the only
	// writer of the presence while an Update runs, so a key's first mutation
	// through it is that key's first mutation at all -- while costing
	// nothing on the Updates that mutate no presence, which is most of them.
	//
	// A slice rather than a map because it holds one entry per presence key
	// the Update actually touched, which is one or two in practice; the
	// linear scans below are cheaper than hashing, and an untouched presence
	// costs no allocation at all.
	previousPresence []prevPresence

	// reversePresenceKeys holds the presence keys marked undoable via
	// presence.WithHistory during this context. A key set again without
	// WithHistory is removed, so only the last Set call for a given key
	// decides whether it is undoable.
	reversePresenceKeys map[string]struct{}
}

// NewContext creates a new instance of Context. The baseline ReversePresence
// rebuilds from is not captured here: the presence proxy reports each key's
// prior value through RecordPreviousPresence as it mutates it. See the
// previousPresence field.
func NewContext(prevID ID, message string, root *crdt.Root) *Context {
	return &Context{
		prevID:  prevID,
		nextID:  prevID.Next(),
		message: message,
		root:    root,
	}
}

// NextID returns the next ID of this context. It will be set to the
// document for the next change.
func (c *Context) NextID() ID {
	if len(c.operations) == 0 {
		// Even if the change has only presence change, the next ID for the document
		// shoule have clocks. For this, we pass the clocks of the previous ID.
		id := c.prevID.Next(true)
		id.lamport = c.prevID.lamport
		id.versionVector = c.prevID.versionVector
		return id
	}

	return c.nextID
}

// ToChange creates a new change of this context.
func (c *Context) ToChange() *Change {
	id := c.nextID

	// NOTE(hackerwins): If this context was created only for presence change,
	if c.IsPresenceOnlyChange() {
		id = c.prevID.Next(true)
	}

	return New(id, c.message, c.operations, c.presenceChange)
}

// IsPresenceOnlyChange returns whether this context is only for presence change or not.
func (c *Context) IsPresenceOnlyChange() bool {
	return len(c.operations) == 0
}

// HasChange returns whether this context has changes.
func (c *Context) HasChange() bool {
	return len(c.operations) > 0 || c.presenceChange != nil
}

// IssueTimeTicket creates a time ticket to be used to create a new operation.
func (c *Context) IssueTimeTicket() *time.Ticket {
	c.delimiter++
	return c.nextID.NewTimeTicket(c.delimiter)
}

// Push pushes a new operations into context queue.
func (c *Context) Push(op operations.Operation) {
	c.operations = append(c.operations, op)
}

// RegisterElement registers the given element to the root.
func (c *Context) RegisterElement(elem crdt.Element) {
	c.root.RegisterElement(elem)
}

// RegisterRemovedElementPair registers the given element pair to hash table.
func (c *Context) RegisterRemovedElementPair(parent crdt.Container, deleted crdt.Element) {
	c.root.RegisterRemovedElementPair(parent, deleted)
}

// RegisterGCPair registers the given GC pair to the root.
func (c *Context) RegisterGCPair(pair crdt.GCPair) {
	c.root.RegisterGCPair(pair)
}

// Acc accumulates the given DataSize to Live size of the root.
func (c *Context) Acc(diff resource.DataSize) {
	c.root.Acc(diff)
}

// AdjustDiffForGCPair adjusts the given diff for the given GCPair to the root.
func (c *Context) AdjustDiffForGCPair(diff *resource.DataSize, pair crdt.GCPair) {
	c.root.AdjustDiffForGCPair(diff, pair)
}

// LastTimeTicket returns the last time ticket issued by this context.
func (c *Context) LastTimeTicket() *time.Ticket {
	return c.nextID.NewTimeTicket(c.delimiter)
}

// SetPresenceChange sets the presence change of the user who made the change.
func (c *Context) SetPresenceChange(presenceChange inner.Change) {
	c.presenceChange = &presenceChange
}

// HasPresenceChange reports whether this context carries a presence change.
func (c *Context) HasPresenceChange() bool {
	return c.presenceChange != nil
}

// DropPresenceChange clears any presence change accumulated on this context.
// The client uses this on documents attached with disable_presence so the
// emitted Change carries operations only.
func (c *Context) DropPresenceChange() {
	c.presenceChange = nil
}

// RecordPreviousPresence records the value key held before this context
// mutated it. Only the first call for a given key has any effect, so what is
// recorded is the value as of context creation however many times the key is
// set afterwards -- which is what JS's up-front snapshot of the whole
// presence means. Every presence mutation calls it, including the ones that
// are not marked undoable: a plain Set followed by one with
// presence.WithHistory has to undo to the value the plain Set overwrote, not
// to the one it wrote.
func (c *Context) RecordPreviousPresence(key, value string, exists bool) {
	if _, ok := c.lookupPreviousPresence(key); ok {
		return
	}

	c.previousPresence = append(c.previousPresence, prevPresence{
		key:    key,
		value:  value,
		exists: exists,
	})
}

// lookupPreviousPresence returns what RecordPreviousPresence recorded for the
// given key, and whether anything was recorded for it at all.
func (c *Context) lookupPreviousPresence(key string) (prevPresence, bool) {
	for _, prev := range c.previousPresence {
		if prev.key == key {
			return prev, true
		}
	}
	return prevPresence{}, false
}

// SetReversePresenceKey records or forgets a presence key for reverse
// tracking. presence.Presence.Set calls this on every Set: when
// addToHistory is true the key is added, so ReversePresence includes it;
// otherwise it is removed, so a later Set of the same key without
// presence.WithHistory opts back out.
func (c *Context) SetReversePresenceKey(key string, addToHistory bool) {
	if addToHistory {
		if c.reversePresenceKeys == nil {
			c.reversePresenceKeys = make(map[string]struct{})
		}
		c.reversePresenceKeys[key] = struct{}{}
		return
	}

	delete(c.reversePresenceKeys, key)
}

// ReversePresence returns what undoing this change would restore for the
// keys marked undoable via presence.WithHistory: the values they held at
// context creation, plus the keys that held nothing then, which undo has to
// remove rather than restore.
//
// The split exists because a key that held nothing has no value to restore.
// Reporting it as the empty string and setting that back would leave the key
// present with an empty value. JS reads `undefined` for the same key and its
// JSON-based deep copy drops it from the Put entirely (context.ts:212-219),
// removing the key -- which is what the absent list reproduces here.
//
// Both results are empty when no presence key was marked undoable.
func (c *Context) ReversePresence() (values inner.Presence, absentKeys []string) {
	if len(c.reversePresenceKeys) == 0 {
		return nil, nil
	}

	for key := range c.reversePresenceKeys {
		// A key reaches reversePresenceKeys only through a proxy mutation,
		// and every mutation records first, so the lookup finding nothing
		// means the key was never mutated -- which cannot happen. Treating it
		// as absent is the conservative reading either way: nothing was
		// recorded for the key, so there is nothing to restore.
		prev, ok := c.lookupPreviousPresence(key)
		if !ok || !prev.exists {
			absentKeys = append(absentKeys, key)
			continue
		}
		if values == nil {
			values = inner.New()
		}
		values.Set(key, prev.value)
	}

	// Map iteration order is randomized; sort so the reverse entry, and
	// anything built from it, is the same on every run.
	sort.Strings(absentKeys)
	return values, absentKeys
}

// ClearReversePresence discards any reverse-presence keys recorded during
// this context. The client uses this alongside DropPresenceChange on
// documents attached with disable_presence, so a dropped presence emit does
// not push a stale undo entry onto the history stack.
func (c *Context) ClearReversePresence() {
	c.reversePresenceKeys = nil
}

// HasOperations reports whether this context has at least one operation.
func (c *Context) HasOperations() bool {
	return len(c.operations) > 0
}

// GCElementPairMap returns the gcElementPairMap for testing purposes.
func (c *Context) GCElementPairMap() map[string]crdt.ElementPair {
	return c.root.GCElementPairMap()
}
