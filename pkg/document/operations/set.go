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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Set represents an operation that stores the value corresponding to the
// given key in the Object.
type Set struct {
	// parentCreatedAt is the creation time of the Object that executes Set.
	parentCreatedAt *time.Ticket

	// key corresponds to the key of the object to set the value.
	key string

	// value is the value of this operation.
	value crdt.Element

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSet creates a new instance of Set.
func NewSet(
	parentCreatedAt *time.Ticket,
	key string,
	value crdt.Element,
	executedAt *time.Ticket,
) *Set {
	return &Set{
		key:             key,
		value:           value,
		parentCreatedAt: parentCreatedAt,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Set) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (ExecutionResult, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)

	obj, ok := parent.(*crdt.Object)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	// During undo/redo, skip rather than execute when obj or any of its
	// ancestors has been concurrently removed (set_operation.ts:81-89).
	if source == OpSourceUndoRedo && isRemovedOrOrphaned(root, obj) {
		return ExecutionResult{}, ErrOperationSkipped
	}

	// The reverse must be built from the value at this key before it is
	// overwritten below (set_operation.ts:91-92): it restores the previous
	// value, or removes the key entirely when there was none.
	//
	// Skipped when the source discards the reverse (see OpSource.NeedsReverse,
	// and the same gate in Edit.Execute): on a remote apply or a server
	// replay the DeepCopy is pure cost, and its error path could abort a
	// change that applied fine before this operation grew a reverse. The
	// forward mutation and every size/GC bookkeeping below stay unconditional.
	var reverseOp Operation
	if source.NeedsReverse() {
		previous := obj.Get(o.key)
		if previous != nil && previous.RemovedAt() == nil {
			copied, err := previous.DeepCopy()
			if err != nil {
				return ExecutionResult{}, err
			}
			reverseOp = NewSet(o.parentCreatedAt, o.key, copied, o.executedAt)
		} else {
			reverseOp = NewRemove(o.parentCreatedAt, o.value.CreatedAt(), o.executedAt)
		}
	}

	value, err := o.value.DeepCopy()
	if err != nil {
		return ExecutionResult{}, err
	}
	// A value carrying the createdAt of a live element anywhere else in the
	// document would take that element's Root.elementMap slot through the
	// RegisterElement below, stranding it. See hijacksLiveElement, which Add
	// and ArraySet consult for the same reason before their own registration.
	//
	// The check is not conditional on the value arriving already removed. A
	// pre-removed value is only the easiest shape of the hijack -- it skips
	// the tombstoning in ElementRHT's losing branch entirely, so it is indexed
	// without the Remove that would otherwise have to accept its tickets -- but
	// a live value that wins its key re-points the same slot on the winning
	// branch, with nothing between it and elementMap at all.
	//
	// Refusing is a no-op that leaves the document exactly as it was, which is
	// also the correct idempotent outcome for a re-applied Set: its value's
	// createdAt already names the live copy registered by the first apply.
	if hijacksLiveElement(root, value) {
		return ExecutionResult{}, nil
	}

	// SetWithExecutedAt uses o.executedAt (rather than value's own createdAt)
	// as the LWW tie-break ticket. For local and remote Sets these are
	// always equal (the json layer issues one fresh ticket for both), so
	// this is behavior-preserving there; for undo/redo restoring an older
	// value under its original createdAt, it is required for the restore to
	// win the LWW comparison at all.
	removed, indexed := obj.SetWithExecutedAt(o.key, value, o.executedAt)

	// A value the object refused is in neither of its member maps, so nothing
	// below may run for it. RegisterElement would charge docSize.Live for an
	// element that hangs off no container and point elementMap at it, taking
	// the slot of whatever live copy already answers to that createdAt -- and
	// UnregisterRemovedElementPair would retire the collection entry of a
	// tombstone that is still indexed, which is precisely the precondition it
	// documents as holding only because SetWithExecutedAt re-pointed the index.
	// Either leaves data nothing can reach and nothing can collect.
	//
	// Refusal means the object is byte-identical to what it was, so the
	// operation is a no-op: no reverse (there is nothing to undo) and not
	// observable. The two reachable causes are a re-applied Set, for which a
	// no-op is the correct idempotent outcome, and a crafted change whose
	// createdAt does not follow the ticket that beat it. Every replica and the
	// server's snapshot replay reach the same decision from the same state.
	if !indexed {
		return ExecutionResult{}, nil
	}

	// NOTE(hackerwins): A Set can restore an element under a createdAt that a
	// tombstone already answers to (set_operation.ts:98-104) -- undoing a
	// Remove re-inserts the removed element under its original identity, and
	// SetWithExecutedAt above has just handed that identity to the restored
	// copy in the object's nodeMapByCreatedAt.
	//
	// The entry that has to follow is the one in gcElementPairMap. Collection
	// resolves it through the index that was just re-pointed, so leaving it
	// makes the next pass purge the restored element instead of the tombstone
	// -- deleting live data, on every replica and in every snapshot the server
	// builds afterwards.
	//
	// Retiring that entry is the whole job, so retire only that entry. The
	// tombstone's other registrations are deliberately left alone: its
	// descendant set can be a strict superset of the restored copy's, since a
	// peer may have added a child into the container after the undoing replica
	// took its copy, and tearing the subtree out of elementMap would take those
	// extra descendants with it, with nothing to put them back. See
	// Root.UnregisterRemovedElementPair.
	//
	// This is a condition on the state of the tree, not on who is applying:
	// a peer receiving the undo, and the server replaying the change log to
	// build a snapshot, reach byte-identical state. Gating it on
	// OpSourceUndoRedo spared only the replica that performed the undo and
	// lost the member everywhere else.
	//
	// The entry has to belong to this object for that to hold. A collection
	// entry names the container the tombstone was removed from, and only the
	// one this Set just displaced from obj's nodeMapByCreatedAt has been
	// re-pointed at live data. An entry registered under the same createdAt by
	// any other container is untouched by this Set and still resolves to its
	// own tombstone, so retiring it would release a charge the document is
	// still carrying and leave a tombstone nothing can ever collect -- the
	// mirror image, on the winning branch, of what the refusal above prevents
	// on the losing one. Root.UnregisterRemovedElementPair takes obj for that
	// reason and does nothing when the entry is someone else's.
	//
	// An ordinary Set carries a freshly issued createdAt, so the lookup
	// normally misses and costs one map read.
	root.UnregisterRemovedElementPair(obj, value.CreatedAt())
	root.RegisterElement(value, obj)
	if removed != nil {
		root.RegisterRemovedElementPair(obj, removed)
	}
	// NOTE(hackerwins): A value that lost the Set is marked removed by
	// SetWithExecutedAt above, before it was registered. RegisterElement is what
	// books it into GC, and registering it as removed a second time here would
	// refund a ticket Live is holding on the one path where RegisterElement
	// leaves it in Live.
	return ExecutionResult{Reverse: reverseOp, Observable: true}, nil
}

// ParentCreatedAt returns the creation time of the Object.
func (o *Set) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Set) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Set) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Set) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// Key returns the key of this operation.
func (o *Set) Key() string {
	return o.key
}

// Value returns the value of this operation.
func (o *Set) Value() crdt.Element {
	return o.value
}
