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

	// isUndoOp marks this TreeEdit as a reverse operation produced by Execute
	// rather than by a user edit. It is local-only state (never on the wire),
	// mirroring Edit.isUndoOp: it gates both ReconcileOperation (only a
	// pending undo/redo entry is reconciled) and the fromIdx/toIdx -> from/to
	// conversion below (a reverse's indices may have been reconciled since
	// this op was built, so its positions are re-derived from them right
	// before executing).
	isUndoOp bool

	// fromIdx/toIdx are this op's own visible-index range, tracked
	// separately from from/to (*crdt.TreePos) because TreePos is
	// identity-based and has no arithmetic: ReconcileOperation shifts a
	// pending undo/redo entry by adjusting these integers, and Execute
	// converts them back into from/to via Tree.FindPos immediately before
	// running, so a remote edit that lands in between is honored. nil for an
	// op that was never built as a reverse (an ordinary forward edit) or
	// whose reverse could not resolve a range (degrades to (0, 0) in
	// NormalizePos, mirroring the JS SDK's undefined case there).
	fromIdx, toIdx *int

	// lastFromIdx/lastToIdx are the visible-index range THIS execution's own
	// forward Tree.Edit call affected, captured immediately before the
	// mutation runs (so tombstoning has not yet collapsed the range). Read by
	// NormalizePos to report the range this op just affected, for reconciling
	// OTHER stacked entries -- e.g. when this op is a genuinely fresh local or
	// remote edit, or a copy-reinsert reverse being replayed. Left nil by the
	// identity-preserving path, which returns before any Tree.Edit call runs.
	lastFromIdx, lastToIdx *int

	// redoSplitLevel is set on the boundary-deletion reverse that undoes a
	// split, and names the level of the split it undoes. It keeps that
	// boundary deletion off both of the paths an ordinary deletion takes when
	// its OWN reverse (the redo) is built: instead of reviving the boundary
	// nodes it tombstoned -- by identity or by copy -- the redo re-splits at
	// the merged position. Local-only state, never on the wire, mirroring
	// TreeEditOperation.redoSplitLevel in the JS SDK.
	//
	// JS distinguishes `undefined` (not a split's undo) from a number; Go uses
	// the zero value for the same distinction, which is exact here because the
	// only producer, toSplitReverseOperation, is reached only for splitLevel >
	// 0 and so never stores a 0.
	redoSplitLevel int

	// insertedContentSize is the visible-index size of the content THIS
	// execution's forward Tree.Edit call accepted (info.InsertedContentSize),
	// mirroring TreeEditOperation.insertedContentSize in the JS SDK. Read by
	// GetContentSize to report to reconciliation. Zero (Go's zero value) for
	// an op that took the identity-preserving path, which never sets it --
	// the same value JS's own contents-less fallback there returns.
	insertedContentSize int
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

			// This op is already an identity-preserving reverse being
			// (re-)executed -- e.g. a redo of an earlier undo. Its own
			// fromIdx/toIdx (if any) are carried into the reverse this
			// execution produces unchanged: they name the range the ORIGINAL
			// forward edit affected, which restoreSpans/retombstoneSpans
			// still address by identity regardless of how far reconciliation
			// has since moved them, mirroring TreeEditOperation.execute's
			// inline identity branch in the JS SDK.
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
				isUndoOp:         true,
				fromIdx:          e.fromIdx,
				toIdx:            e.toIdx,
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

		// A reverse being (re-)executed may have had its fromIdx/toIdx
		// reconciled since it was built (a remote edit landed while it sat
		// on a history stack). Re-derive from/to from them now, immediately
		// before the mutation, so the reconciled range -- not the stale
		// positions this op was constructed with -- is what actually runs.
		// Mirrors TreeEditOperation.execute's "for undo ops: convert stored
		// integer indices to CRDTTreePos" step in the JS SDK.
		if e.isUndoOp && e.fromIdx != nil && e.toIdx != nil {
			fromPos, err := obj.FindPos(*e.fromIdx)
			if err != nil {
				return nil, err
			}
			e.from = fromPos
			if *e.fromIdx == *e.toIdx {
				e.to = fromPos
			} else {
				toPos, err := obj.FindPos(*e.toIdx)
				if err != nil {
					return nil, err
				}
				e.to = toPos
			}
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

		// Mirrors JS's `this.insertedContentSize = insertedContentSize`:
		// reassigned on every execution that reaches here, reported via
		// GetContentSize for the applyChanges reconciliation loop.
		e.insertedContentSize = info.InsertedContentSize

		// The pre-edit visible-index range this execution affected, read
		// straight off info rather than recomputed here: Tree.Edit captures
		// PreEditFromIdx after Phase 3 (Range Narrowing), the latest point
		// that still sees the tree before Phase 5 tombstones anything in the
		// range, and RemovedSize once every phase has run -- both at points
		// this package cannot reach from outside Tree.Edit. Reported by
		// NormalizePos (when this op is not itself carrying reconciled
		// indices of its own) so the applyChanges reconciliation loop can
		// adjust OTHER stacked entries against the range this execution
		// just affected.
		lastFromIdx := info.PreEditFromIdx
		lastToIdx := info.PreEditFromIdx + info.RemovedSize
		e.lastFromIdx, e.lastToIdx = &lastFromIdx, &lastToIdx

		// info.Removed and info.PreTombstoned name live tombstones still
		// linked into obj, not copies, so the reverse operation's content is
		// deep-copied inside this call — the way JS does inside the same
		// execute(). A later SplitText mutates in place and splits tombstones
		// too, so holding onto them past this point would let a subsequent
		// edit truncate the captured content.
		return e.selectReverseOperation(obj, contents, info)

	default:
		return nil, ErrNotApplicableDataType
	}
}

// selectReverseOperation picks which builder reverses the edit that just ran,
// mirroring tree_edit_operation.ts:470-487.
//
// A splitLevel 0 edit goes to toReverseOperation. A splitting edit is reversed
// by a boundary deletion, but only when the split is all it did: an edit that
// also inserted or removed would need its reverse to undo both halves at once,
// and neither builder produces that, so it gets no reverse rather than a
// partial one.
//
// The purity test reads e.contents, the content this operation carries, rather
// than the accepted copy Execute passes down -- the two have the same length
// (Tree.Edit drops duplicates from its own copy of the slice, not from the
// caller's) and e.contents is what JS's `this.contents` names. info.Removed and
// info.PreTombstoned partition the set JS calls removedNodes, which includes
// pre-tombstoned nodes while info.Removed alone does not, so both are checked
// to test the same emptiness JS tests.
func (e *TreeEdit) selectReverseOperation(
	tree *crdt.Tree,
	inserted []*crdt.TreeNode,
	info crdt.TreeEditReverseInfo,
) (Operation, error) {
	if e.splitLevel == 0 {
		return e.toReverseOperation(tree, inserted, info, info.PreEditFromIdx)
	}

	isPureSplit := len(e.contents) == 0 &&
		len(info.Removed) == 0 &&
		len(info.PreTombstoned) == 0
	if !isPureSplit {
		return nil, nil
	}

	return e.toSplitReverseOperation(tree, info.PreEditFromIdx)
}

// toReverseOperation builds the operation that undoes this edit. It has four
// outcomes, in the order tree_edit_operation.ts:526-665 takes them:
//
//  1. Ordinarily, an identity-preserving reverse: revive the nodes this edit
//     removed and re-remove the ones it inserted, both by original identity.
//  2. For the boundary deletion that undid a split (redoSplitLevel set), a
//     re-split at the merged position — the redo of that split.
//  3. For a merge, a split at the merge position: a merge deletes element
//     boundaries, so re-creating them is what undoes it.
//  4. Otherwise, the copy-reinsert fallback: delete the range this edit
//     inserted and re-insert a copy of what it removed. Reversing by copy is
//     what makes ReissueContentIDs necessary — see there.
//
// The fallback's positions are read off the post-edit tree, mirroring
// tree_edit_operation.ts:640-665, which computes them as
// findPos(preEditFromIdx) and findPos(preEditFromIdx + insertedContentSize).
// Go's Tree.Edit does now report the equivalent (TreeEditReverseInfo.
// PreEditFromIdx), but outcome 4 below deliberately does NOT use it for
// fromPos/toPos: it derives its anchor from the nodes the edit actually
// touched instead (immediately before the content the tree accepted, or --
// with nothing accepted -- immediately before the first node this edit
// tombstoned). That anchor is a filed, open divergence from JS (see
// "a Tree reverse can delete live neighbours when its content was born
// tombstoned" in the cross-SDK defects doc) which this comment is not
// re-litigating; preFromIdx below is passed to outcomes 1-3 and the no-op
// case only, never to outcome 4.
//
// Only a splitLevel 0 edit reaches here at all: Execute sends a splitting one
// to toSplitReverseOperation instead, branching on the same condition JS
// branches on before calling this (tree_edit_operation.ts:475-487).
//
// preFromIdx is this edit's own pre-mutation visible-index anchor
// (info.PreEditFromIdx, which Tree.Edit captures after Phase 3, the same
// point JS's own preEditFromIdx is captured at) -- unrelated to the
// post-edit, node-derived anchor idx computed below for outcome 4's
// fromPos/toPos. Outcomes 1-3 and the no-op case use it directly, as a
// zero-width point, mirroring how JS uses its own preEditFromIdx the same
// way in the identical branches (tree_edit_operation.ts:543-598, :610-616).
func (e *TreeEdit) toReverseOperation(
	tree *crdt.Tree,
	inserted []*crdt.TreeNode,
	info crdt.TreeEditReverseInfo,
	preFromIdx int,
) (Operation, error) {
	// Reverse this edit by identity: revive the nodes it removed
	// (restoreSpans) and re-remove the nodes it inserted (retombstoneSpans),
	// both under their ORIGINAL identities instead of copy-reinserting. That
	// is what makes two clients concurrently undoing one deletion converge:
	// reviving a node already revived is a no-op, while two copy-reinserting
	// reverses each mint their own nodes and both survive.
	//
	// Tree.Edit only fills these spans when they fully describe the edit
	// (SpansComplete), so this never fires for the merge and born-tombstoned
	// cases handled below. The redoSplitLevel guard is what keeps a split's own
	// boundary-deletion undo off this path: that deletion is an ordinary one as
	// far as Tree.Edit is concerned, so it could fill the spans here, and
	// reviving the boundary nodes it tombstoned is not the redo of a split --
	// re-splitting is (outcome 2).
	if e.redoSplitLevel == 0 && (len(info.RemovedSpans) > 0 || len(info.InsertedSpans) > 0) {
		fromIdx, toIdx := preFromIdx, preFromIdx
		return &TreeEdit{
			parentCreatedAt:  e.parentCreatedAt,
			from:             e.from,
			to:               e.to,
			restoreSpans:     info.RemovedSpans,
			restoreMode:      crdt.RestoreModeRestore,
			retombstoneSpans: info.InsertedSpans,
			isUndoOp:         true,
			fromIdx:          &fromIdx,
			toIdx:            &toIdx,
		}, nil
	}

	// This edit IS the boundary deletion that undid a split, so its own reverse
	// -- the redo -- re-splits at the position the deletion merged, rather than
	// re-inserting the boundary nodes it tombstoned. Mirrors
	// tree_edit_operation.ts:564-580. The split it produces is itself a pure
	// split, so Execute reverses it through toSplitReverseOperation again and
	// the undo/redo cycle closes.
	if e.redoSplitLevel > 0 {
		return e.splitReverseAt(tree, preFromIdx, e.redoSplitLevel)
	}

	// A merge deletes element boundaries and moves their children into the
	// merge target, so its reverse is a split, not a content re-insertion:
	// re-inserting the emptied elements would restore shells whose children now
	// live elsewhere. One split level per boundary the merge consumed. Mirrors
	// tree_edit_operation.ts:585-598.
	if info.MergeLevel > 0 {
		return e.splitReverseAt(tree, preFromIdx, info.MergeLevel)
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
		fromIdx, toIdx := preFromIdx, preFromIdx
		return &TreeEdit{
			parentCreatedAt: e.parentCreatedAt,
			from:            e.from,
			to:              e.from,
			restoreMode:     crdt.RestoreModeNone,
			isUndoOp:        true,
			fromIdx:         &fromIdx,
			toIdx:           &toIdx,
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
	// (tree_edit_operation.ts:610-616). It sits ahead of the copy below for
	// the same reason JS puts it there: the skip case does no DeepCopy, and a
	// copy failing cannot turn a skip into a failed Execute.
	if idx+insertedSize > tree.Root().Len() {
		return nil, nil
	}

	contents, err := reverseContents(info.Removed, info.PreTombstoned)
	if err != nil {
		return nil, err
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

	// The reverse's own reconciliation anchor is this SAME node-derived idx,
	// not preFromIdx: fromPos/toPos above are built from it too, and Execute
	// re-derives from/to from fromIdx/toIdx on every future (re-)execution
	// (see the "convert stored integer indices" step there). Using a
	// different value here would make this op's position and its
	// reconciliation index disagree about where it points -- reconciliation
	// would silently stop tracking the range Execute actually operates on.
	fromIdx, toIdx := idx, idx+insertedSize
	return &TreeEdit{
		parentCreatedAt: e.parentCreatedAt,
		from:            fromPos,
		to:              toPos,
		contents:        contents,
		restoreMode:     crdt.RestoreModeNone,
		isUndoOp:        true,
		fromIdx:         &fromIdx,
		toIdx:           &toIdx,
	}, nil
}

// toSplitReverseOperation builds the operation that undoes THIS split, a
// boundary deletion. Ported from tree_edit_operation.ts:680-712.
//
// A split creates element boundaries without removing anything: one close tag
// plus one open tag per level, so 2*splitLevel tree-index tokens. Deleting
// exactly those tokens merges the split elements back together, which is why
// the reverse is an ordinary splitLevel 0 edit rather than a new operation
// kind — every existing reconciliation and redo path applies to it unchanged.
// See docs/design/tree-split-undo-redo.md.
//
// preFromIdx is info.PreEditFromIdx, captured inside Tree.Edit after Phase 3
// and so naming the position in the PRE-split tree that the split ran at.
// That is the same index the boundary tokens start at in the POST-split tree
// (a split inserts its boundary at the split point and shifts nothing to its
// left), which is what lets one index serve both as the anchor read off the
// post-edit tree here and as the reconciliation anchor stored on the reverse.
// JS captures and uses it at exactly this point too.
func (e *TreeEdit) toSplitReverseOperation(tree *crdt.Tree, preFromIdx int) (Operation, error) {
	fromIdx := preFromIdx
	toIdx := preFromIdx + 2*e.splitLevel

	// The split had no visible effect — e.g. a concurrent deletion tombstoned
	// the element it split, so the boundary it created occupies no visible
	// index. Deleting the range anyway would take out live content to the
	// right of it.
	if toIdx > tree.Root().Len() {
		return nil, nil
	}

	fromPos, err := tree.FindPos(fromIdx)
	if err != nil {
		return nil, err
	}
	toPos, err := tree.FindPos(toIdx)
	if err != nil {
		return nil, err
	}

	// redoSplitLevel is what keeps this op's OWN reverse on the re-split path
	// rather than reviving the boundary nodes it is about to tombstone; see
	// toReverseOperation's outcome 2.
	return &TreeEdit{
		parentCreatedAt: e.parentCreatedAt,
		from:            fromPos,
		to:              toPos,
		restoreMode:     crdt.RestoreModeNone,
		isUndoOp:        true,
		fromIdx:         &fromIdx,
		toIdx:           &toIdx,
		redoSplitLevel:  e.splitLevel,
	}, nil
}

// splitReverseAt builds a zero-width split of the given level at preFromIdx, the
// shape both branches of toReverseOperation that reverse a merge produce: the
// redo of a split whose undo merged its boundary away (redoSplitLevel), and the
// undo of a user-initiated merge (MergeLevel). JS writes the two out separately
// (tree_edit_operation.ts:564-580 and :585-598) but they differ only in which
// level they carry.
//
// The op carries no content — a split creates boundaries rather than inserting
// nodes — and its range is the single point preFromIdx, which is where the
// merge this reverses left the joined content. Both indices are that point, so
// Execute's fromIdx/toIdx conversion resolves a zero-width range, and
// reconciliation moves the point rather than resizing a range.
func (e *TreeEdit) splitReverseAt(
	tree *crdt.Tree,
	preFromIdx int,
	splitLevel int,
) (Operation, error) {
	pos, err := tree.FindPos(preFromIdx)
	if err != nil {
		return nil, err
	}

	fromIdx, toIdx := preFromIdx, preFromIdx
	return &TreeEdit{
		parentCreatedAt: e.parentCreatedAt,
		from:            pos,
		to:              pos,
		splitLevel:      splitLevel,
		restoreMode:     crdt.RestoreModeNone,
		isUndoOp:        true,
		fromIdx:         &fromIdx,
		toIdx:           &toIdx,
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

// NormalizePos returns the visible-index range of this operation, mirroring
// TreeEditOperation.normalizePos in the JS SDK (tree_edit_operation.ts:
// 715-733). For an undo/redo entry carrying its own (possibly reconciled)
// fromIdx/toIdx, that range is returned as-is. Otherwise the range this
// operation's own most recent forward execution affected (lastFromIdx/
// lastToIdx) is returned. Neither is available for an operation that has
// never executed, or whose reverse took the identity-preserving path (which
// returns before either is captured) -- (0, 0) is returned then, matching
// the JS SDK's own fallback. That degenerate case is harmless: GetContentSize
// degrades to 0 in lockstep (see there), so every ReconcileOperation case
// below computes a net-zero shift from it.
func (e *TreeEdit) NormalizePos() (int, int) {
	if e.isUndoOp && e.fromIdx != nil && e.toIdx != nil {
		return *e.fromIdx, *e.toIdx
	}
	if e.lastFromIdx != nil && e.lastToIdx != nil {
		return *e.lastFromIdx, *e.lastToIdx
	}
	return 0, 0
}

// GetContentSize returns the visible-index size of the content this
// operation's own most recent forward execution accepted, mirroring
// TreeEditOperation.getContentSize in the JS SDK (tree_edit_operation.ts:
// 826-836). JS falls back to summing this.contents' padded sizes when
// insertedContentSize was never captured; that fallback is not ported here
// because it is unreachable through Go's own call site (applyChanges calls
// this only on an operation from Execute's executed list, which has always
// already run -- either through the identity-preserving path, whose
// insertedContentSize stays at Go's zero value and whose contents is always
// nil, or through the path that sets insertedContentSize unconditionally,
// even to 0). Keeping the dead branch out avoids asserting a size Go can
// never actually compute from -- e.Contents() is *crdt.TreeNode, not the
// padded-length-bearing type JS's fallback reduces over.
func (e *TreeEdit) GetContentSize() int {
	return e.insertedContentSize
}

// ReconcileOperation adjusts this TreeEdit's fromIdx/toIdx in place so a
// pending undo/redo entry stays correct after a remote edit executes on the
// same Tree. It mirrors TreeEditOperation.reconcileOperation in the JS SDK
// (tree_edit_operation.ts:735-822), the same six-case overlap logic
// Edit.ReconcileOperation implements for Text, over integer indices instead
// of RGATreeSplitNodePos offsets. remoteFrom, remoteTo, and contentSize
// describe the remote edit in the same visible-index domain as NormalizePos.
//
// Identity-addressed restore/retombstone ops (restoreSpans/retombstoneSpans
// set) locate their nodes by TreeNodeID, never by fromIdx/toIdx, so index
// reconciliation must not touch them -- mirrors Edit.ReconcileOperation's
// identical guard for Text. This method never reads or writes either span
// field.
func (e *TreeEdit) ReconcileOperation(remoteFrom, remoteTo, contentSize int) {
	if !e.isUndoOp {
		return
	}
	if len(e.restoreSpans) > 0 || len(e.retombstoneSpans) > 0 {
		return
	}
	if e.fromIdx == nil || e.toIdx == nil {
		return
	}
	if remoteFrom > remoteTo {
		return
	}

	remoteRangeLen := remoteTo - remoteFrom
	localFrom := *e.fromIdx
	localTo := *e.toIdx

	apply := func(na, nb int) {
		na, nb = max(0, na), max(0, nb)
		e.fromIdx, e.toIdx = &na, &nb
	}

	// Case 1: remote edit is to the left of the undo range.
	// [--remote--]  [--undo--]
	if remoteTo <= localFrom {
		apply(localFrom-remoteRangeLen+contentSize, localTo-remoteRangeLen+contentSize)
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
		apply(localFrom, localTo-remoteRangeLen+contentSize)
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
