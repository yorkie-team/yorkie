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
	"errors"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ErrCannotReissueSplittingEdit occurs when ReissueContentIDs is asked to
// re-identify the content of an edit that also splits. The tickets it takes
// would collide with the ones the split itself consumes; see the method.
var ErrCannotReissueSplittingEdit = errors.New("cannot reissue content ids on a splitting edit")

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

			// The reverse of an identity-preserving edit keeps both span sets
			// and only flips the direction, so undoing a restore re-removes
			// exactly the nodes it revived, still under their original
			// identities. Copying their content into a fresh insertion here
			// would defeat the whole point of the restore path. Built before
			// the mutation, from state the mutation does not touch.
			reverseOp := &TreeEdit{
				parentCreatedAt:  e.parentCreatedAt,
				from:             e.from,
				to:               e.to,
				restoreSpans:     e.restoreSpans,
				restoreMode:      flipRestoreMode(e.restoreMode),
				retombstoneSpans: e.retombstoneSpans,
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
			return reverseOp, nil
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
		pairs, diff, info, err := obj.Edit(
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

		// info.Removed and info.PreTombstoned name live tombstones still
		// linked into obj, not copies, so the reverse operation's content is
		// deep-copied inside this call — the way JS does inside the same
		// execute(). A later SplitText mutates in place and splits tombstones
		// too, so holding onto them past this point would let a subsequent
		// edit truncate the captured content.
		return e.toReverseOperation(obj, contents, info)

	default:
		return nil, ErrNotApplicableDataType
	}
}

// toReverseOperation builds the operation that undoes this edit. It has three
// outcomes, in the order tree_edit_operation.ts:526-665 takes them:
//
//  1. Ordinarily, an identity-preserving reverse: revive the nodes this edit
//     removed and re-remove the ones it inserted, both by original identity.
//  2. For a merge, no reverse at all — the reverse of a merge is a split.
//  3. Otherwise, the copy-reinsert fallback: delete the range this edit
//     inserted and re-insert a copy of what it removed. Reversing by copy is
//     what makes ReissueContentIDs necessary — see there.
//
// The fallback's positions are read off the post-edit tree, mirroring
// tree_edit_operation.ts:640-665, which computes them as
// findPos(preEditFromIdx) and findPos(preEditFromIdx + insertedContentSize).
// Go's Tree.Edit does not report preEditFromIdx, so the anchor is derived from
// the nodes the edit actually touched instead: it sits immediately before the
// content the tree accepted, or — with nothing accepted — immediately before
// the first node this edit tombstoned, which names the same point once those
// tombstones stop counting towards the index.
//
// Only splitLevel 0 is reversed, including by outcome 1: a split's reverse is
// a boundary deletion rather than a content re-insertion, and is not built yet
// (JS branches on the same condition before calling this at all).
func (e *TreeEdit) toReverseOperation(
	tree *crdt.Tree,
	inserted []*crdt.TreeNode,
	info crdt.TreeEditReverseInfo,
) (Operation, error) {
	if e.splitLevel != 0 {
		return nil, nil
	}

	// Reverse this edit by identity: revive the nodes it removed
	// (restoreSpans) and re-remove the nodes it inserted (retombstoneSpans),
	// both under their ORIGINAL identities instead of copy-reinserting. That
	// is what makes two clients concurrently undoing one deletion converge:
	// reviving a node already revived is a no-op, while two copy-reinserting
	// reverses each mint their own nodes and both survive.
	//
	// Tree.Edit only fills these spans when they fully describe the edit
	// (SpansComplete), so this never fires for the merge and
	// born-tombstoned cases handled below. JS additionally excludes an op
	// tagged with redoSplitLevel here; Go builds no split reverses yet, so it
	// has no such op to exclude.
	if len(info.RemovedSpans) > 0 || len(info.InsertedSpans) > 0 {
		return &TreeEdit{
			parentCreatedAt:  e.parentCreatedAt,
			from:             e.from,
			to:               e.to,
			restoreSpans:     info.RemovedSpans,
			restoreMode:      crdt.RestoreModeRestore,
			retombstoneSpans: info.InsertedSpans,
		}, nil
	}

	// A merge deletes element boundaries and moves their children into the
	// merge target, so its reverse is a split, not a content re-insertion:
	// re-inserting the emptied elements would restore shells whose children
	// now live elsewhere. Building that split reverse needs the split
	// machinery a splitting edit's own reverse needs, which does not exist
	// yet, so a merging edit produces no reverse rather than a wrong one.
	if info.MergeLevel > 0 {
		return nil, nil
	}

	// Only the content the tree accepted counts. Content reusing an ID the
	// tree already holds is dropped on the way in, so a reverse range covering
	// it would delete a neighbour on redo. Content tombstoned on the way into
	// a removed parent still counts towards the size (Tree.Edit measures it
	// while the content is detached, as JS does) but cannot anchor the range,
	// since it has no position in the visible tree.
	var lastLive *crdt.TreeNode
	insertedSize := info.InsertedContentSize
	for _, content := range inserted {
		if content.Index.Parent == nil || content.IsRemoved() {
			continue
		}
		lastLive = content
	}

	contents, err := reverseContents(info.Removed, info.PreTombstoned)
	if err != nil {
		return nil, err
	}

	var anchor *crdt.TreeNode
	switch {
	case lastLive != nil:
		anchor = lastLive
	case len(info.Removed) > 0:
		// The first newly tombstoned node in document order. Its own parent
		// may be a tombstone too, in which case ToIndex resolves the position
		// of the topmost removed ancestor — the same anchor.
		anchor = info.Removed[0]
	default:
		// This edit neither removed nor inserted anything: the reverse is an
		// ordinary no-op edit anchored where this one ran. Kept rather than
		// dropped so an edit is always undoable, matching the JS SDK, which
		// collapses to the same zero-width range here.
		//
		// The range has to be zero-width, not this edit's own: reaching here
		// with a wide range means everything it covered was already tombstoned
		// or its content was refused, and undoing by deleting that range again
		// would take out whatever is live inside it.
		return &TreeEdit{
			parentCreatedAt: e.parentCreatedAt,
			from:            e.from,
			to:              e.from,
			restoreMode:     crdt.RestoreModeNone,
		}, nil
	}

	if anchor.Index.Parent == nil {
		return nil, nil
	}
	// ToIndex reports the index just after a live node and just before a
	// tombstoned one, which is why the live case subtracts the inserted size
	// and the tombstoned case does not.
	idx, err := tree.ToIndex(anchor.Index.Parent.Value, anchor)
	if err != nil {
		return nil, err
	}
	if idx < 0 {
		return nil, nil
	}
	if lastLive != nil {
		idx -= insertedSize
	}

	// The size above was measured while the content was still detached, so it
	// counts content the tree tombstoned on the way into a concurrently
	// removed parent. That content is nowhere in the visible tree, so the
	// range runs past the end of it — which is how JS recognizes an edit that
	// had no visible effect and skips its reverse
	// (tree_edit_operation.ts:610-616).
	if idx+insertedSize > tree.Root().Len() {
		return nil, nil
	}

	fromPos, err := tree.FindPos(idx)
	if err != nil {
		return nil, err
	}
	toPos := fromPos
	if insertedSize > 0 {
		if toPos, err = tree.FindPos(idx + insertedSize); err != nil {
			return nil, err
		}
	}

	return &TreeEdit{
		parentCreatedAt: e.parentCreatedAt,
		from:            fromPos,
		to:              toPos,
		contents:        contents,
		restoreMode:     crdt.RestoreModeNone,
	}, nil
}

// reverseContents deep-copies the nodes a copy-reinserting reverse should
// carry: the top-level ones among those the edit removed, each stripped of the
// descendants that were already tombstoned before the edit ran. Without that
// filter, undoing a parent delete resurrects the user's earlier independent
// deletes, which accumulate across undo/redo cycles.
func reverseContents(
	removed []*crdt.TreeNode,
	preTombstoned map[string]struct{},
) ([]*crdt.TreeNode, error) {
	topLevel := topLevelRemoved(removed, preTombstoned)
	if len(topLevel) == 0 {
		return nil, nil
	}

	contents := make([]*crdt.TreeNode, 0, len(topLevel))
	for _, node := range topLevel {
		clone, err := node.CloneForReinsert(preTombstoned)
		if err != nil {
			return nil, err
		}
		contents = append(contents, clone)
	}

	return contents, nil
}

// topLevelRemoved filters removed down to the nodes whose parent this edit did
// not also tombstone; the rest come along as descendants of those.
//
// Parent membership is tested against the UNION of removed and preTombstoned.
// JS's nodesToBeRemoved (tree_edit_operation.ts:622-627) includes
// pre-tombstoned nodes, while Tree.Edit's removed excludes them, so testing
// against removed alone would promote a live descendant of an already
// tombstoned parent to top level — and undo would resurrect it at the wrong
// depth. removed itself never holds a pre-tombstoned node, so no such check is
// needed on the node itself.
func topLevelRemoved(
	removed []*crdt.TreeNode,
	preTombstoned map[string]struct{},
) []*crdt.TreeNode {
	inRemoved := make(map[*crdt.TreeNode]struct{}, len(removed))
	for _, node := range removed {
		inRemoved[node] = struct{}{}
	}

	var topLevel []*crdt.TreeNode
	for _, node := range removed {
		if node.Index.Parent == nil {
			topLevel = append(topLevel, node)
			continue
		}

		parent := node.Index.Parent.Value
		if _, ok := inRemoved[parent]; ok {
			continue
		}
		if _, ok := preTombstoned[parent.IDString()]; ok {
			continue
		}
		topLevel = append(topLevel, node)
	}

	return topLevel
}

// ReissueContentIDs gives every node this operation inserts a fresh identity.
//
// A reverse operation that reverses a deletion by re-inserting a copy of the
// removed nodes carries their original IDs, so executing it would put two
// nodes under one ID — the ambiguity that makes a position anchored there
// resolve differently on different replicas. Undo already re-identifies a
// restored value elsewhere: Add and ArraySet both take the fresh ticket in
// executeUndoRedo. This is the tree's counterpart, called from the same place
// so the IDs come from the change the undo creates.
//
// A restore-mode reverse is left alone: it revives nodes under their original
// identity by design, which is what makes concurrent undos of one deletion
// converge rather than duplicate.
func (e *TreeEdit) ReissueContentIDs(issueTimeTicket func() *time.Ticket) error {
	if len(e.contents) == 0 || e.restoreMode != crdt.RestoreModeNone {
		return nil
	}

	// The tickets taken here start at executedAt.Delimiter() + 1 and run one
	// per node, while Execute simulates the tickets an element split consumes
	// starting at executedAt.Delimiter() + len(contents) + 1. The two ranges
	// overlap as soon as content has descendants, so this only holds while no
	// content-bearing reverse splits — which is every reverse
	// toReverseOperation builds, all of them splitLevel 0.
	if e.splitLevel != 0 {
		return ErrCannotReissueSplittingEdit
	}

	for _, content := range e.contents {
		content.ReissueIDs(issueTimeTicket)
	}

	return nil
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
