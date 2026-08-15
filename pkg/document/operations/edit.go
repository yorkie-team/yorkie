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
	"strings"
	"unicode/utf16"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Edit is an operation representing editing Text. Most of the same as
// Edit, but with additional style properties, attributes.
type Edit struct {
	// parentCreatedAt is the creation time of the Text that executes
	// Edit.
	parentCreatedAt *time.Ticket

	// from represents the start point of the editing range.
	from *crdt.RGATreeSplitNodePos

	// to represents the end point of the editing range.
	to *crdt.RGATreeSplitNodePos

	// content is the content of text added when editing.
	content string

	// attributes represents the text style.
	attributes map[string]string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket

	// isUndoOp marks this Edit as a reverse operation produced by Execute
	// rather than by a user edit. It is local-only state (never on the wire):
	// a reverse's positions were recorded against an older split chain, so
	// they are refined onto the current one before it executes.
	isUndoOp bool

	// restoreSpans carries the removed content tagged with its original
	// character identities, letting the server revive or re-remove it
	// under those identities on the client's instruction. Empty for
	// ordinary edits.
	restoreSpans []*crdt.RestoreSpan

	// restoreMode selects the identity-preserving path (restore vs
	// retombstone). RestoreModeNone for ordinary edits.
	restoreMode crdt.RestoreMode

	// retombstoneSpans is the companion span set for an identity-preserving
	// reverse op: restoreSpans is content the reversed edit removed (to
	// revive), retombstoneSpans is content it inserted (to re-remove). Both
	// are addressed by original identity so a revived neighbor keeps its
	// relative order across chained undo/redo. restoreMode selects direction.
	retombstoneSpans []*crdt.RestoreSpan
}

// NewEdit creates a new instance of Edit.
func NewEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.RGATreeSplitNodePos,
	to *crdt.RGATreeSplitNodePos,
	content string,
	attributes map[string]string,
	executedAt *time.Ticket,
) *Edit {
	return &Edit{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		content:         content,
		attributes:      attributes,
		executedAt:      executedAt,
		restoreMode:     crdt.RestoreModeNone,
	}
}

// NewRestoreEdit creates an Edit that revives (RestoreModeRestore) or
// re-removes (RestoreModeRetombstone) content under its original node
// identities, carried in spans. Restore edits have no content or
// attributes of their own; the spans describe what to re-establish.
func NewRestoreEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.RGATreeSplitNodePos,
	to *crdt.RGATreeSplitNodePos,
	executedAt *time.Ticket,
	restoreSpans []*crdt.RestoreSpan,
	restoreMode crdt.RestoreMode,
	retombstoneSpans []*crdt.RestoreSpan,
) *Edit {
	return &Edit{
		parentCreatedAt:  parentCreatedAt,
		from:             from,
		to:               to,
		executedAt:       executedAt,
		restoreSpans:     restoreSpans,
		restoreMode:      restoreMode,
		retombstoneSpans: retombstoneSpans,
	}
}

// Execute executes this operation on the given document(`root`).
func (e *Edit) Execute(root *crdt.Root, _ OpSource, versionVector time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *crdt.Text:
		// Restore/retombstone spans carry client-supplied node identities
		// (createdAt) that the server materializes into authoritative state.
		// Reject any identity the acting change could not causally have
		// observed, so a client cannot forge a node under another actor's
		// clock or advance it. See validateRestoreIdentities.
		if err := validateRestoreIdentities(e.restoreSpans, versionVector); err != nil {
			return nil, err
		}
		if err := validateRestoreIdentities(e.retombstoneSpans, versionVector); err != nil {
			return nil, err
		}

		if len(e.restoreSpans) > 0 || len(e.retombstoneSpans) > 0 {
			// Identity-preserving reverse op. restoreMode selects the
			// direction: an undo (RestoreModeRestore) revives restoreSpans and
			// re-removes retombstoneSpans; the redo (RestoreModeRetombstone)
			// does the opposite. Both sets are revived/removed by their
			// original identity, never re-inserted as fresh nodes, so relative
			// order is preserved across chained undo/redo. Must mirror the JS
			// EditOperation.execute path so server snapshots match clients.
			toRestore, toRetombstone := e.restoreSpans, e.retombstoneSpans
			if e.restoreMode == crdt.RestoreModeRetombstone {
				toRestore, toRetombstone = e.retombstoneSpans, e.restoreSpans
			}

			// The reverse of an identity-preserving edit keeps both span sets
			// and only flips the direction, so undoing a restore re-removes
			// exactly the characters it revived, still under their original
			// identities. Copying their content into a fresh insertion here
			// would defeat the whole point of the restore path. Built before
			// the mutation, from state the mutation does not touch.
			reverseOp := &Edit{
				parentCreatedAt:  e.parentCreatedAt,
				from:             e.from,
				to:               e.to,
				isUndoOp:         true,
				restoreSpans:     e.restoreSpans,
				restoreMode:      flipRestoreMode(e.restoreMode),
				retombstoneSpans: e.retombstoneSpans,
			}

			// 1. Re-remove the content the reversed edit inserted (by identity).
			if len(toRetombstone) > 0 {
				pairs, diff := obj.Retombstone(toRetombstone, e.executedAt)
				for _, pair := range pairs {
					root.RegisterGCPair(pair)
					root.AdjustDiffForGCPair(&diff, pair)
				}
				root.Acc(diff)
			}

			// 2. Revive the content the reversed edit removed (by identity).
			if len(toRestore) > 0 {
				untombstoned, recreated, stillTombstoned := obj.Restore(
					toRestore, e.executedAt, e.from)

				// Register the still-tombstoned split remainders (which include
				// any split-born target) BEFORE un-registering the
				// un-tombstoned targets, so each target's GCOnlySize is booked
				// before its full size is moved GC->Live. Reversing the order
				// would leak GC size.
				for _, pair := range stillTombstoned {
					root.RegisterGCPair(pair)
				}

				var diff resource.DataSize
				for _, node := range untombstoned {
					root.UnregisterGCPair(crdt.GCPair{Parent: obj.RGATreeSplit(), Child: node})
					diff.Add(node.DataSize())
				}
				for _, node := range recreated {
					diff.Add(node.DataSize())
				}
				root.Acc(diff)
			}

			return reverseOp, nil
		}

		// A reverse's positions were recorded against the chain as it stood
		// when the reverse was built; splits since then have moved them.
		if e.isUndoOp {
			from, err := obj.RefinePos(e.from)
			if err != nil {
				return nil, err
			}
			to, err := obj.RefinePos(e.to)
			if err != nil {
				return nil, err
			}
			e.from, e.to = from, to
		}

		_, pairs, diff, removedValues, removedSpans, err := obj.Edit(
			e.from, e.to, e.content, e.attributes, e.executedAt, versionVector)
		for _, pair := range pairs {
			root.RegisterGCPair(pair)
			root.AdjustDiffForGCPair(&diff, pair)
		}
		root.Acc(diff)
		if err != nil {
			return nil, err
		}

		// The anchor is normalized after the edit, against the chain the
		// reverse will actually run on.
		fromPos, err := obj.NormalizePos(e.from)
		if err != nil {
			return nil, err
		}

		return e.toReverseOperation(removedValues, fromPos, removedSpans), nil

	default:
		return nil, ErrNotApplicableDataType
	}
}

// toReverseOperation builds the operation that undoes this edit from what the
// edit removed (removedValues, removedSpans) and what it inserted (content),
// anchored at the normalized fromPos.
func (e *Edit) toReverseOperation(
	removedValues []string,
	fromPos *crdt.RGATreeSplitNodePos,
	removedSpans []crdt.RestoreSpan,
) Operation {
	if len(removedSpans) > 0 || len(e.content) > 0 {
		// Reverse any edit by identity: revive what it removed
		// (restoreSpans) and re-remove what it inserted (retombstoneSpans),
		// rather than copy-reinserting or position-deleting. A fresh
		// copy-reinserted node sorts ahead of an as-yet-unrevived neighbour
		// and corrupts order across chained undo/redo; a position-based
		// delete of the inserted range reconciles onto the wrong node under a
		// concurrent remote edit. Identity addressing avoids both.
		var restoreSpans []*crdt.RestoreSpan
		if len(removedSpans) > 0 {
			restoreSpans = make([]*crdt.RestoreSpan, 0, len(removedSpans))
			for i := range removedSpans {
				restoreSpans = append(restoreSpans, &removedSpans[i])
			}
		}

		var insertedSpans []*crdt.RestoreSpan
		if len(e.content) > 0 {
			// The insertion is one run born under this edit's own ticket, so
			// it is addressed as [0, len) of that identity. Its attributes
			// are irrelevant: re-removing a run never reads them.
			insertedSpans = []*crdt.RestoreSpan{{
				CreatedAt: e.executedAt,
				Start:     0,
				End:       len(utf16.Encode([]rune(e.content))),
				Content:   e.content,
			}}
		}

		return &Edit{
			parentCreatedAt:  e.parentCreatedAt,
			from:             fromPos,
			to:               fromPos,
			isUndoOp:         true,
			restoreSpans:     restoreSpans,
			restoreMode:      crdt.RestoreModeRestore,
			retombstoneSpans: insertedSpans,
		}
	}

	// Neither removed nor inserted anything: the reverse is an ordinary
	// no-op edit. Kept rather than dropped so an edit is always undoable,
	// matching the JS SDK.
	//
	// Everything below evaluates to empty in practice. Reaching here requires
	// len(removedSpans) == 0, and Text.Edit fills removedValues and
	// removedSpans in one loop, so removedValues is empty too. The shape is
	// kept as JS has it (edit_operation.ts:300-323) rather than collapsed to
	// a constant, so a future divergence in what Text.Edit reports shows up
	// as a behavior difference rather than as silently unreachable code that
	// someone already deleted.

	// JS keys this on removedValues.length === 1 and reads the attributes off
	// the removed value itself. Go's removedValues carries only text, so the
	// attributes come from the parallel removedSpans entry -- the length
	// check on it is a bounds guard for that indexing, not a second
	// condition JS is missing.
	var attributes map[string]string
	if len(removedValues) == 1 && len(removedSpans) > 0 {
		attributes = removedSpans[0].Attributes
	}

	return &Edit{
		parentCreatedAt: e.parentCreatedAt,
		from:            fromPos,
		to: crdt.NewRGATreeSplitNodePos(
			fromPos.ID(),
			fromPos.RelativeOffset()+len(utf16.Encode([]rune(e.content))),
		),
		// NOTE: joining a slice that is always empty here, and the only use
		// of the strings import. It mirrors JS's removedValues.map(...).join('')
		// and is deliberate -- do not drop it as dead code without also
		// revisiting the block comment above.
		content:     strings.Join(removedValues, ""),
		attributes:  attributes,
		isUndoOp:    true,
		restoreMode: crdt.RestoreModeNone,
	}
}

// flipRestoreMode returns the direction that undoes the given one: undoing a
// restore re-removes, and undoing a re-removal restores.
func flipRestoreMode(mode crdt.RestoreMode) crdt.RestoreMode {
	if mode == crdt.RestoreModeRetombstone {
		return crdt.RestoreModeRestore
	}
	return crdt.RestoreModeRetombstone
}

// validateRestoreIdentities rejects restore/retombstone spans whose node
// identity the acting change could not causally have observed. A legitimate
// restore only revives content the client previously saw (and therefore
// deleted), so each span's createdAt lamport must not exceed the actor's
// clock as known to the change's version vector, and the actor must be
// present in it. An empty version vector marks the trusted local path
// (json package application), where no such check applies.
func validateRestoreIdentities(
	spans []*crdt.RestoreSpan,
	versionVector time.VersionVector,
) error {
	if len(spans) == 0 {
		return nil
	}
	createdAts := make([]*time.Ticket, 0, len(spans))
	for _, span := range spans {
		createdAts = append(createdAts, span.CreatedAt)
	}
	return validateRestoreTickets(createdAts, versionVector)
}

// validateRestoreTickets rejects any restore identity the acting change could
// not causally have observed (actor absent from the version vector, or a
// lamport beyond the actor's known clock). An empty version vector marks the
// trusted local path (json application). Shared by the Text and Tree restore
// paths.
func validateRestoreTickets(
	createdAts []*time.Ticket,
	versionVector time.VersionVector,
) error {
	if len(createdAts) == 0 || len(versionVector) == 0 {
		return nil
	}

	for _, createdAt := range createdAts {
		known, ok := versionVector.Get(createdAt.ActorID())
		if !ok || createdAt.Lamport() > known {
			return ErrUnknownRestoreIdentity
		}
	}
	return nil
}

// From returns the start point of the editing range.
func (e *Edit) From() *crdt.RGATreeSplitNodePos {
	return e.from
}

// To returns the end point of the editing range.
func (e *Edit) To() *crdt.RGATreeSplitNodePos {
	return e.to
}

// ExecutedAt returns execution time of this operation.
func (e *Edit) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *Edit) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *Edit) SetExecutedAt(executedAt *time.Ticket) {
	e.executedAt = executedAt
}

// ParentCreatedAt returns the creation time of the Text.
func (e *Edit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Content returns the content of Edit.
func (e *Edit) Content() string {
	return e.content
}

// Attributes returns the attributes of this Edit.
func (e *Edit) Attributes() map[string]string {
	return e.attributes
}

// RestoreSpans returns the identity-preserving restore payload.
func (e *Edit) RestoreSpans() []*crdt.RestoreSpan {
	return e.restoreSpans
}

// RestoreMode returns the identity-preserving mode of this Edit.
func (e *Edit) RestoreMode() crdt.RestoreMode {
	return e.restoreMode
}

// RetombstoneSpans returns the companion span set (content the reversed edit
// inserted) for an identity-preserving reverse op.
func (e *Edit) RetombstoneSpans() []*crdt.RestoreSpan {
	return e.retombstoneSpans
}

// ContentLen returns the length of Content in UTF-16 code units, the unit
// RGATreeSplit positions are measured in.
func (e *Edit) ContentLen() int {
	return len(utf16.Encode([]rune(e.content)))
}

// NormalizePos converts this Edit's from/to positions into absolute offsets
// from the head of the parent Text's physical split chain. It mirrors
// EditOperation.normalizePos in the JS SDK (edit_operation.ts:327-347),
// which throws on the failure paths below; Go reports them via the third
// return value instead.
//
// It is only ever called on an operation that has already executed against
// root (see the applyChanges reconciliation loop in document.go), so the
// parent Text is guaranteed to resolve and failure here would indicate an
// invariant violation elsewhere in the caller. But (0, 0) is also a
// legitimate normalized position, so degrading to it silently on failure
// would be indistinguishable from that legitimate case -- a caller that
// skipped the check could reconcile every stacked entry in the document by
// a bogus amount. The bool return makes "did not normalize" impossible to
// mistake for "normalized to offset 0".
func (e *Edit) NormalizePos(root *crdt.Root) (int, int, bool) {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	text, ok := parent.(*crdt.Text)
	if !ok {
		return 0, 0, false
	}

	fromPos, err := text.NormalizePos(e.from)
	if err != nil {
		return 0, 0, false
	}
	toPos, err := text.NormalizePos(e.to)
	if err != nil {
		return 0, 0, false
	}

	return fromPos.RelativeOffset(), toPos.RelativeOffset(), true
}

// ReconcileOperation adjusts this Edit's from/to positions in place so a
// pending undo/redo entry stays correct after a remote edit executes on the
// same Text. It mirrors EditOperation.reconcileOperation in the JS SDK
// (edit_operation.ts:349-410). remoteFrom, remoteTo, and contentLen describe
// the remote edit in the same normalized offset domain as NormalizePos.
//
// NOTE: restoreSpans/retombstoneSpans address content by identity
// (createdAt + offset), so from/to are never used to locate the restored
// range itself. But from is also the fallback anchor for when every related
// piece has been GC'd, so it still needs to track concurrent remote edits
// like any other undo position -- only the identity payload
// (restoreSpans/retombstoneSpans) must stay untouched, and this method never
// reads or writes either.
func (e *Edit) ReconcileOperation(remoteFrom, remoteTo, contentLen int) {
	if !e.isUndoOp {
		return
	}
	if remoteFrom > remoteTo {
		return
	}

	remoteRangeLen := remoteTo - remoteFrom
	localFrom := e.from.RelativeOffset()
	localTo := e.to.RelativeOffset()

	apply := func(na, nb int) {
		e.from = crdt.NewRGATreeSplitNodePos(e.from.ID(), max(0, na))
		e.to = crdt.NewRGATreeSplitNodePos(e.to.ID(), max(0, nb))
	}

	// Case 1: remote edit is to the left of the undo range.
	// [--remote--]  [--undo--]
	if remoteTo <= localFrom {
		apply(localFrom-remoteRangeLen+contentLen, localTo-remoteRangeLen+contentLen)
		return
	}

	// Case 2: remote edit is to the right of the undo range.
	// [--undo--]  [--remote--]
	if localTo <= remoteFrom {
		return
	}

	// Case 3: undo range is contained within the remote range.
	// [-------remote-------]
	//      [--undo--]
	if remoteFrom <= localFrom && localTo <= remoteTo && remoteFrom != remoteTo {
		apply(remoteFrom, remoteFrom)
		return
	}

	// Case 4: remote range is contained within the undo range.
	//      [--remote--]
	// [---------undo---------]
	if localFrom <= remoteFrom && remoteTo <= localTo && localFrom != localTo {
		apply(localFrom, localTo-remoteRangeLen+contentLen)
		return
	}

	// Case 5: remote range overlaps the start of the undo range.
	// [---remote---]
	//      [---undo---]
	if remoteFrom < localFrom && localFrom < remoteTo && remoteTo < localTo {
		apply(remoteFrom, remoteFrom+(localTo-remoteTo))
		return
	}

	// Case 6: remote range overlaps the end of the undo range.
	//      [---remote---]
	// [---undo---]
	if localFrom < remoteFrom && remoteFrom < localTo && localTo < remoteTo {
		apply(localFrom, remoteFrom)
	}
}
