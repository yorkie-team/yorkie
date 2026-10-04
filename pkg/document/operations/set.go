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
	if parent == nil {
		return skipUnresolvedTarget(source)
	}

	obj, ok := parent.(*crdt.Object)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	// During undo/redo, skip rather than execute when obj or any of its
	// ancestors has been concurrently removed (set_operation.ts:81-89).
	if source == OpSourceUndoRedo && isRemovedOrOrphaned(root, obj) {
		return ExecutionResult{}, ErrOperationSkipped
	}

	// NOTE(hackerwins): The payload's own createdAt now decides control flow
	// below -- a value whose createdAt another element already answers to can
	// be refused by the object (ElementRHT.refusesLoser), which leaves a
	// subtree the document keeps addressable and charges to neither side of
	// docSize. The ticket arrives verbatim off the wire (api/converter's
	// fromSet reads parent_created_at, executed_at and the element's createdAt
	// straight from the request, and sanitizeElement checks none of them), so
	// without a check here a client picks which branch the server takes by
	// naming any element it can see.
	//
	// A collision a legitimate history produces has exactly two shapes, and
	// both pass:
	//
	//   - the ticket answers to a tombstone. That is an undo restoring the
	//     value under the createdAt it was removed as, which is the whole
	//     reason Set tolerates a reused ticket at all.
	//   - the ticket answers to the live element sitting at this very key.
	//     That is the concurrent-restore shape: another replica restored the
	//     same value first, and this copy is about to lose the LWW compare.
	//
	// Anything else names a live element this Set is not restoring -- a
	// different key, a different object, or a descendant of either -- and
	// taking it in would either strand that element or hand the sender an
	// unaccounted subtree. It is refused before a single map is touched.
	//
	// Refused, not failed, on everything but a local Set: the server stores a
	// pushed change before executing it, so a hard error would be replayed out
	// of the change log forever. See refuseInUseIdentity. The general
	// push-boundary validation this stands in for is tracked at
	// yorkie-team/yorkie#2081.
	if identityInUse(root, o.value, obj.Get(o.key)) {
		return refuseInUseIdentity(source)
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
	// SetWithExecutedAt uses o.executedAt (rather than value's own createdAt)
	// as the LWW tie-break ticket. For local and remote Sets these are
	// always equal (the json layer issues one fresh ticket for both), so
	// this is behavior-preserving there; for undo/redo restoring an older
	// value under its original createdAt, it is required for the restore to
	// win the LWW comparison at all.
	removed, indexed := obj.SetWithExecutedAt(o.key, value, o.executedAt)

	// A value the object refused is in neither of its member maps, so nothing
	// below may run for it. RegisterElement would point elementMap at it,
	// taking the slot of the copy that already answers to that createdAt, and
	// UnregisterRemovedElementPair would retire the collection entry of a
	// tombstone that is still indexed. Only a losing value is ever refused
	// (ElementRHT.refusesLoser), and a losing Set changes nothing visible on
	// any replica, so the operation did not apply. Every replica and the
	// server's snapshot replay reach the same decision from the same state.
	//
	// It reports that the way the Operation contract requires a decline to be
	// reported -- ErrOperationSkipped, as the concurrently-removed-target path
	// above does -- not a nil error. Change.Execute then keeps the operation
	// out of both the executed list and the reverse operations, which is what
	// stops a refused Set from contributing a half-built reverse: a nil error
	// with an empty ExecutionResult reads as "applied, nothing to undo", and
	// an undo whose Set is refused would push a redo entry describing work
	// that never happened (Document.executeUndoRedo).
	//
	// What it does leave behind is the copy's elementMap slots. The replicas
	// that met the two restores in the opposite order keep them -- there this
	// copy took the key first, was registered, and was then evicted and
	// released -- and a descendant only this copy carries exists on exactly one
	// side, so dropping it here would hard-fail an operation addressed at it on
	// these replicas alone. Root.AdoptRefusedCopy indexes exactly the slots
	// nothing already answers to, at no cost, which is where the other order
	// ends up.
	if !indexed {
		root.AdoptRefusedCopy(value)
		return ExecutionResult{}, ErrOperationSkipped
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
	// The entry has to belong to this object for that to hold. Only the
	// tombstone this Set just displaced from obj's nodeMapByCreatedAt has been
	// re-pointed at live data; an entry another container registered under
	// the same createdAt still resolves to its own tombstone, so
	// Root.UnregisterRemovedElementPair takes obj and leaves such an entry
	// alone.
	//
	// An ordinary Set carries a freshly issued createdAt, so the lookup
	// normally misses and costs one map read.
	root.UnregisterRemovedElementPair(obj, value.CreatedAt())

	// NOTE(hackerwins): The occupant this Set evicted can be another copy
	// under value's own createdAt: two replicas undoing concurrent overwrites
	// of one key both restore the original value, and the newer restore
	// evicts the older one. SetWithExecutedAt has then dropped the evicted
	// copy from both of obj's maps, so it is orphaned the moment it is
	// removed, exactly like the tombstone retired just above.
	//
	// Booking it as an ordinary removed pair would file it under a createdAt
	// key that value now answers to. Nothing can collect it through obj, and
	// the next removal under that createdAt -- a newer Set evicting value --
	// overwrites the entry, leaving the copy's GC charge in docSize forever on
	// the replicas that met the restores in that order and on no other. So it
	// is booked and retired at once: RegisterRemovedElementPair moves its
	// size out of Live with the removal-ticket refund, and
	// UnregisterRemovedElementPair releases it, leaving value as the only copy
	// accounted for. RegisterElement below then retires the release records,
	// as value's subtree takes the copy's elementMap slots over.
	if removed != nil && removed.CreatedAt().Compare(value.CreatedAt()) == 0 {
		root.RegisterRemovedElementPair(obj, removed)
		root.UnregisterRemovedElementPair(obj, removed.CreatedAt())
		removed = nil
	}

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
