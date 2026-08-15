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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TreeEdit is an operation representing Tree editing.
type TreeEdit struct {
	// parentCreatedAt is the creation time of the Tree that executes
	// TreeEdit.
	parentCreatedAt *time.Ticket

	// fromPos represents the start point of the editing range.
	from *crdt.TreePos

	// toPos represents the end point of the editing range.
	to *crdt.TreePos

	// contents is the content of tree added when editing.
	contents []*crdt.TreeNode

	// splitLevel is the level of the split.
	splitLevel int

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket

	// restoreSpans/restoreMode/retombstoneSpans carry identity-preserving
	// Tree undo/redo, mirroring Edit. restoreMode selects direction: an undo
	// (Restore) revives restoreSpans and re-removes retombstoneSpans by
	// identity; the redo (Retombstone) does the opposite. RestoreModeNone for
	// ordinary edits. Reverse ops are generated client-side; the server only
	// executes the mode the wire op specifies.
	restoreSpans     []*crdt.TreeRestoreSpan
	restoreMode      crdt.RestoreMode
	retombstoneSpans []*crdt.TreeRestoreSpan

	// splitTickets carries the tickets the originating replica issued for the
	// nodes an element split creates, in issue order. A replica applying the
	// operation consumes them instead of reconstructing them, so neither side
	// depends on the other's allocation staying in step. Empty for a change
	// written before the field existed, which falls back to the simulation
	// that reconstructed them from executedAt and the content count.
	splitTickets []*time.Ticket
}

// NewTreeEdit creates a new instance of TreeEdit.
func NewTreeEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	contents []*crdt.TreeNode,
	splitLevel int,
	executedAt *time.Ticket,
) *TreeEdit {
	return &TreeEdit{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		contents:        contents,
		splitLevel:      splitLevel,
		executedAt:      executedAt,
		restoreMode:     crdt.RestoreModeNone,
	}
}

// NewRestoreTreeEdit creates a TreeEdit that revives (RestoreModeRestore) or
// re-removes (RestoreModeRetombstone) tree nodes under their original
// identities, carried in spans.
func NewRestoreTreeEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	executedAt *time.Ticket,
	restoreSpans []*crdt.TreeRestoreSpan,
	restoreMode crdt.RestoreMode,
	retombstoneSpans []*crdt.TreeRestoreSpan,
) *TreeEdit {
	return &TreeEdit{
		parentCreatedAt:  parentCreatedAt,
		from:             from,
		to:               to,
		executedAt:       executedAt,
		restoreSpans:     restoreSpans,
		restoreMode:      restoreMode,
		retombstoneSpans: retombstoneSpans,
	}
}

// Execute executes this operation on the given `CRDTRoot`.
func (e *TreeEdit) Execute(root *crdt.Root, _ OpSource, versionVector time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *crdt.Tree:
		// Identity-preserving restore/retombstone path (mirrors Edit).
		// restoreMode selects direction; an undo revives restoreSpans and
		// re-removes retombstoneSpans, the redo does the opposite. Both span
		// sets carry client-supplied identities materialized into authoritative
		// state, so reject any the acting change could not causally observe.
		if len(e.restoreSpans) > 0 || len(e.retombstoneSpans) > 0 {
			if err := validateTreeRestoreIdentities(e.restoreSpans, versionVector); err != nil {
				return nil, err
			}
			if err := validateTreeRestoreIdentities(e.retombstoneSpans, versionVector); err != nil {
				return nil, err
			}

			toRestore, toRetombstone := e.restoreSpans, e.retombstoneSpans
			if e.restoreMode == crdt.RestoreModeRetombstone {
				toRestore, toRetombstone = e.retombstoneSpans, e.restoreSpans
			}

			var diff resource.DataSize
			// 1. Re-remove (retombstone) by identity. Isolating a straddling
			// piece splits it (live-split overhead accounted to diff).
			retombstonePairs, retombstoneDiff, err := obj.Retombstone(toRetombstone, e.executedAt)
			if err != nil {
				return nil, err
			}
			diff.Add(retombstoneDiff)
			for _, pair := range retombstonePairs {
				root.RegisterGCPair(pair)
				root.AdjustDiffForGCPair(&diff, pair)
			}
			// 2. Revive (restore) by identity. Isolating a range out of a
			// straddling piece can split off born-removed remainders as pending
			// GC pairs; register them FIRST so a split-born un-tombstoned target
			// is walked gc->live correctly by the UnregisterGCPair below. For an
			// un-tombstoned node (removedAt already cleared by Restore)
			// UnregisterGCPair removes its size from docSize.GC, and diff.Add
			// books the same size into Live — the node just became visible.
			// Recreated nodes are brand new, so they only need the Live
			// addition, plus any live-split overhead. Mirrors Text restore.
			untombstoned, recreated, restorePairs, restoreDiff, err := obj.Restore(toRestore)
			if err != nil {
				return nil, err
			}
			for _, pair := range restorePairs {
				root.RegisterGCPair(pair)
			}
			for _, node := range untombstoned {
				root.UnregisterGCPair(crdt.GCPair{Parent: obj, Child: node})
				diff.Add(node.DataSize())
			}
			diff.Add(restoreDiff)
			for _, node := range recreated {
				diff.Add(node.DataSize())
			}
			root.Acc(diff)
			return nil, nil
		}

		var contents []*crdt.TreeNode
		var err error
		if len(e.Contents()) != 0 {
			for _, content := range e.Contents() {
				var clone *crdt.TreeNode

				clone, err = content.DeepCopy()
				if err != nil {
					return nil, err
				}

				contents = append(contents, clone)
			}

		}
		pairs, diff, err := obj.Edit(
			e.from,
			e.to,
			contents,
			e.splitLevel,
			e.executedAt,
			// Splitting an element creates nodes that need tickets. The
			// originating replica issued them and carries them here, so this
			// hands them back in the same order.
			//
			// A change written before the field existed carries none, and falls
			// back to reconstructing them from executedAt and the number of
			// top-level contents. That reconstruction is wrong whenever content
			// has descendants — each of those consumed a ticket too — which is
			// why the tickets are carried now; see
			// docs/design/tree-content-identity.md.
			func() func() *time.Ticket {
				issued := 0
				delimiter := e.executedAt.Delimiter()
				if contents != nil {
					delimiter += uint32(len(contents))
				}
				return func() *time.Ticket {
					if issued < len(e.splitTickets) {
						ticket := e.splitTickets[issued]
						issued++
						return ticket
					}

					delimiter++
					return time.NewTicket(
						e.executedAt.Lamport(),
						delimiter,
						e.executedAt.ActorID(),
					)
				}
			}(),
			versionVector,
		)
		for _, pair := range pairs {
			root.RegisterGCPair(pair)
			root.AdjustDiffForGCPair(&diff, pair)
		}
		root.Acc(diff)
		if err != nil {
			return nil, err
		}

	default:
		return nil, ErrNotApplicableDataType
	}

	return nil, nil
}

// FromPos returns the start point of the editing range.
func (e *TreeEdit) FromPos() *crdt.TreePos {
	return e.from
}

// ToPos returns the end point of the editing range.
func (e *TreeEdit) ToPos() *crdt.TreePos {
	return e.to
}

// SetActor sets the given actor to this operation.
func (e *TreeEdit) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *TreeEdit) SetExecutedAt(executedAt *time.Ticket) {
	e.executedAt = executedAt
}

// ParentCreatedAt returns the creation time of the Text.
func (e *TreeEdit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Contents returns the content of Edit.
func (e *TreeEdit) Contents() []*crdt.TreeNode {
	return e.contents
}

// SplitLevel returns the level of the split.
func (e *TreeEdit) SplitLevel() int {
	return e.splitLevel
}

// SplitTickets returns the tickets issued for the nodes an element split
// created, in issue order. Empty when the operation predates the field.
func (e *TreeEdit) SplitTickets() []*time.Ticket {
	return e.splitTickets
}

// SetSplitTickets records the tickets issued for the nodes an element split
// created. The originating replica calls this after executing the edit, so
// every other replica can use them instead of reconstructing them.
func (e *TreeEdit) SetSplitTickets(tickets []*time.Ticket) {
	e.splitTickets = tickets
}

// ExecutedAt returns execution time of this operation.
func (e *TreeEdit) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// RestoreSpans returns the identity-preserving restore payload, if any.
func (e *TreeEdit) RestoreSpans() []*crdt.TreeRestoreSpan {
	return e.restoreSpans
}

// RestoreMode returns the identity-preserving mode of this op.
func (e *TreeEdit) RestoreMode() crdt.RestoreMode {
	return e.restoreMode
}

// RetombstoneSpans returns the companion span set, if any.
func (e *TreeEdit) RetombstoneSpans() []*crdt.TreeRestoreSpan {
	return e.retombstoneSpans
}

// validateTreeRestoreIdentities rejects any restore span whose node identity
// the acting change could not causally have observed (shared rule with the
// Text restore path).
func validateTreeRestoreIdentities(
	spans []*crdt.TreeRestoreSpan,
	versionVector time.VersionVector,
) error {
	if len(spans) == 0 {
		return nil
	}
	createdAts := make([]*time.Ticket, 0, len(spans))
	for _, span := range spans {
		createdAts = append(createdAts, span.ID.CreatedAt)
	}
	return validateRestoreTickets(createdAts, versionVector)
}
