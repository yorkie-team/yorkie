/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package converter

import (
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// NormalizeStoredOperations repairs, in place, the shapes FromOperations
// rejects that a change persisted before those checks existed could still
// carry. It exists because FromOperations sits on two paths with opposite
// requirements: it decodes operations arriving from a client, where rejecting
// malformed input is the point, and it also decodes operations read back from
// storage (ChangeInfo.ToChange), where rejecting anything makes the document
// holding that change permanently unloadable. Validation added at the wire
// boundary therefore applies retroactively to data already written under the
// looser rules, and only the stored side calls this.
//
// Whether any such change exists cannot be answered by a query -- operations
// are persisted as opaque protobuf blobs -- so the population is unknown
// rather than known-empty. Normalizing costs nothing if it is empty and
// avoids an unrecoverable read failure if it is not.
//
// Each repair restores what the field meant before the check, except where
// that meaning was itself a crash; see the individual comments. A rejection
// with no repair here is only correct when the accepted shape used to fault
// on read anyway, since there is no earlier behavior left to reproduce.
//
// Almost every ticket rejection is of that kind, which is why almost none of
// them has a counterpart below. fromRequiredTimeTicket refuses an omitted
// ticket on an operation, fromElement refuses one on the element a
// Set/Add/ArraySet carries, and fromTextNodePos/fromTreeNodeID refuse a nil
// createdAt on a node id. Accepting any of them used to hand a nil
// *time.Ticket to Ticket.Key() or Ticket.Compare(), both of which read off the
// pointer -- so a stored change shaped that way already panicked the process
// on the load that decoded it, not merely on the wire. Refusing it downgrades
// that crash to a load error on one document. Nor is a repair available: a
// ticket is (lamport, delimiter, actor) and the message carries no record of
// what the missing one was, so any value invented here would be a different
// operation on every replica that invented it.
//
// Increase is the one exception, and fillIncreaseValueCreatedAt below is its
// repair: its value is decoded by the same fromElement but is never keyed by
// its createdAt, so a stored Increase with no created_at used to load and
// execute fine. That one does have a repair, and it has to, or the new
// rejection would strand a change that was previously harmless.
//
// Apart from it, only clamps -- which have exactly one defensible value --
// appear below.
func NormalizeStoredOperations(pbOps []*api.Operation) {
	for _, pbOp := range pbOps {
		if pbEdit := pbOp.GetEdit(); pbEdit != nil {
			clampTextNodePos(pbEdit.From)
			clampTextNodePos(pbEdit.To)
		}

		if pbStyle := pbOp.GetStyle(); pbStyle != nil {
			clampTextNodePos(pbStyle.From)
			clampTextNodePos(pbStyle.To)
		}

		if pbInc := pbOp.GetIncrease(); pbInc != nil {
			fillIncreaseValueCreatedAt(pbInc)
		}

		if pbTreeStyle := pbOp.GetTreeStyle(); pbTreeStyle != nil {
			clampTreePos(pbTreeStyle.From)
			clampTreePos(pbTreeStyle.To)
		}

		pbTreeEdit := pbOp.GetTreeEdit()
		if pbTreeEdit == nil {
			continue
		}

		// A negative split level was inert before it was rejected: nothing
		// read it beyond the split loop, which does nothing for a
		// non-positive level. Clamping to zero reproduces that exactly, and
		// keeps the level from sizing an inverted range in the
		// boundary-deletion reverse that now reads it.
		if pbTreeEdit.SplitLevel < 0 {
			pbTreeEdit.SplitLevel = 0
		}

		clampTreePos(pbTreeEdit.From)
		clampTreePos(pbTreeEdit.To)
		for _, pbNodes := range pbTreeEdit.Contents {
			if pbNodes == nil {
				continue
			}
			for _, pbNode := range pbNodes.Content {
				clampTreeNodeIDsOf(pbNode)
			}
		}
		clampSpanTreeNodeIDs(pbTreeEdit.RestoreSpans)
		clampSpanTreeNodeIDs(pbTreeEdit.RetombstoneSpans)

		dropUndatedAttrs(pbTreeEdit.RestoreSpans)
		dropUndatedAttrs(pbTreeEdit.RetombstoneSpans)
	}
}

// fillIncreaseValueCreatedAt gives an Increase's delta the created_at
// fromElement now requires, copying the operation's own executed_at.
//
// Unlike a Set, an Add or an ArraySet, an Increase never registers its value
// anywhere: Increase.Execute reads value.Value() and hands the ticket to
// crdt.NewPrimitive, which stores the pointer without dereferencing it, and
// Counter.Increase/IncreaseDedup only look at the number. Nothing calls
// Key() or Compare() on it, so a stored Increase with no created_at loaded
// and executed correctly before fromElement started requiring one -- which
// makes it the single ticket rejection with prior behavior left to reproduce,
// and the single one that would otherwise strand a document.
//
// executed_at is the defensible fill precisely because the ticket is inert:
// it is read from the same message, so every replica normalizing this change
// derives the same value rather than inventing one, and the value it derives
// is the ticket the issuing client drew from the very same change.
func fillIncreaseValueCreatedAt(pbInc *api.Operation_Increase) {
	if pbInc.Value == nil || pbInc.Value.CreatedAt != nil || pbInc.ExecutedAt == nil {
		return
	}

	pbInc.Value.CreatedAt = pbInc.ExecutedAt
}

// clampTreeNodeID pulls a negative tree node id offset back to zero.
//
// An offset counts UTF-16 code units inside the insertion its createdAt
// names, so a negative one never resolved to anything: findFloorNode walks to
// the first id at or below it and lands on the insertion's own head, which is
// offset zero. Clamping states that outcome instead of leaving an id no
// replica can execute, and it is the only repair available -- the offset
// carries no record of what it was meant to be.
func clampTreeNodeID(pbID *api.TreeNodeID) {
	if pbID != nil && pbID.Offset < 0 {
		pbID.Offset = 0
	}
}

// clampTreePos clamps both ids a tree position is built from.
func clampTreePos(pbPos *api.TreePos) {
	if pbPos == nil {
		return
	}

	clampTreeNodeID(pbPos.ParentId)
	clampTreeNodeID(pbPos.LeftSiblingId)
}

// clampTreeNodeIDsOf clamps every id a single tree node carries, including
// the insertion-order and merge links a snapshot-shaped node brings with it.
func clampTreeNodeIDsOf(pbNode *api.TreeNode) {
	if pbNode == nil {
		return
	}

	clampTreeNodeID(pbNode.Id)
	clampTreeNodeID(pbNode.InsPrevId)
	clampTreeNodeID(pbNode.InsNextId)
	clampTreeNodeID(pbNode.MergedFrom)
}

// clampSpanTreeNodeIDs clamps a restore span's own id together with the
// parent and sibling anchors it resolves against.
func clampSpanTreeNodeIDs(pbSpans []*api.TreeRestoreSpan) {
	for _, pbSpan := range pbSpans {
		if pbSpan == nil {
			continue
		}

		clampTreeNodeID(pbSpan.Id)
		clampTreeNodeID(pbSpan.ParentId)
		clampTreeNodeID(pbSpan.LeftSiblingId)
		clampTreeNodeID(pbSpan.RightSiblingId)
	}
}

// clampTextNodePos pulls a text position's negative offsets back to zero, for
// the same reason clampTreeNodeID does: both count UTF-16 code units into an
// insertion, getAbsoluteID sums them, and a negative sum floor-resolves to the
// insertion's head regardless. Zero is that head, stated explicitly.
func clampTextNodePos(pbPos *api.TextNodePos) {
	if pbPos == nil {
		return
	}

	if pbPos.Offset < 0 {
		pbPos.Offset = 0
	}
	if pbPos.RelativeOffset < 0 {
		pbPos.RelativeOffset = 0
	}
}

// dropUndatedAttrs removes restore-span attributes carrying no updatedAt.
//
// This one deliberately does not reproduce the prior behavior: fromRHT stores
// whatever fromTimeTicket returns, which is nil for a nil ticket rather than
// an error, so such an attribute used to reach the RHT and panic on the first
// comparison deep inside the restore path. Dropping the attribute loses one
// style entry on a span; keeping it loses the server. An attribute with no
// updatedAt cannot participate in RHT's last-writer-wins resolution anyway,
// which is what makes dropping it the closest thing to a meaning it has.
func dropUndatedAttrs(pbSpans []*api.TreeRestoreSpan) {
	for _, pbSpan := range pbSpans {
		if pbSpan == nil {
			continue
		}

		for key, attr := range pbSpan.Attributes {
			if attr == nil || attr.UpdatedAt == nil {
				delete(pbSpan.Attributes, key)
			}
		}
	}
}
