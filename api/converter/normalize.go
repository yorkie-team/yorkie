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
	goerrors "errors"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
)

// FromStoredOperations decodes operations read back from storage
// (ChangeInfo.ToChange), where FromOperations' job is the opposite of what it
// is on the wire: rejecting one makes the document holding that change
// permanently unloadable, so validation added at the wire boundary has to be
// undone here for the data already written under the looser rules.
//
// It repairs what can be repaired (NormalizeStoredOperations) and, for the
// rejections no repair exists for -- a time ticket a well-formed operation
// always carries is simply absent -- drops the operation. Dropping loses
// nothing a loadable document ever had: such an operation could never have been
// applied anywhere, since the nil ticket faults inside Execute (nil-keyed RHT
// lookups, Ticket.Compare) rather than being tolerated. The wire path keeps
// rejecting all of it.
//
// The filtering pass costs nothing unless a stored operation actually hits one
// of those rejections, which is why it runs only after the ordinary decode has
// reported ErrMissingTicket.
func FromStoredOperations(pbOps []*api.Operation) ([]operations.Operation, error) {
	NormalizeStoredOperations(pbOps)

	ops, err := FromOperations(pbOps)
	if err == nil {
		return ops, nil
	}
	if !goerrors.Is(err, ErrMissingTicket) {
		return nil, err
	}

	return FromOperations(withoutUndatedOperations(pbOps))
}

// SanitizeStoredOperations applies the same repair-and-drop pass as
// FromStoredOperations, but yields the protobuf operations rather than the
// decoded models.
//
// It exists for the pull path, which forwards stored operations to clients
// without ever decoding them into models (ServerPack.ToPBChangePack). Without
// it the server would tolerate on its own read path exactly the shapes it then
// hands to every pulling client to reject: the client decodes what it receives
// through the strict wire path (FromChangePack -> FromOperations), so a stored
// operation missing a required ticket would fail the pull for the very
// documents FromStoredOperations exists to keep loadable. Sanitizing here keeps
// the two views of a change in agreement.
func SanitizeStoredOperations(pbOps []*api.Operation) []*api.Operation {
	NormalizeStoredOperations(pbOps)

	// Same shape as FromStoredOperations: the drop pass runs only once the
	// ordinary decode has reported ErrMissingTicket, so intact changes -- every
	// change, unless legacy data says otherwise -- pay one decode and no more.
	if _, err := FromOperations(pbOps); err != nil && goerrors.Is(err, ErrMissingTicket) {
		return withoutUndatedOperations(pbOps)
	}

	return pbOps
}

// withoutUndatedOperations drops the operations FromOperations rejects for an
// absent required time ticket, leaving every other rejection to surface.
//
// It asks fromOperation rather than re-listing the required fields so the two
// paths cannot drift: whatever the wire boundary decides is required is exactly
// what the stored path drops.
func withoutUndatedOperations(pbOps []*api.Operation) []*api.Operation {
	kept := make([]*api.Operation, 0, len(pbOps))
	for _, pbOp := range pbOps {
		if _, err := fromOperation(pbOp); err != nil && goerrors.Is(err, ErrMissingTicket) {
			continue
		}

		kept = append(kept, pbOp)
	}

	return kept
}

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
// Only TreeEdit is touched because every repairable check lives on it. Each
// repair restores what the field meant before the check, except where that
// meaning was itself a crash; see the individual comments. Rejections no repair
// exists for are handled by dropping the operation in FromStoredOperations,
// which is what stored decoding should call rather than this directly.
func NormalizeStoredOperations(pbOps []*api.Operation) {
	for _, pbOp := range pbOps {
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

		pbTreeEdit.Contents = withoutEmptyContents(pbTreeEdit.Contents)
		pbTreeEdit.SplitTickets = splitTicketsUnlessUndated(pbTreeEdit.SplitTickets)

		dropUndatedAttrs(pbTreeEdit.RestoreSpans)
		dropUndatedAttrs(pbTreeEdit.RetombstoneSpans)
	}
}

// splitTicketsUnlessUndated returns the carried split tickets, or none at all
// if any one of them is absent.
//
// Truncating at the first absent entry instead reads as the safer repair and is
// not. The list is consumed in order by TreeEdit.Execute's issueTimeTicket,
// whose fallback -- reconstructing a ticket from executedAt's delimiter plus
// the number of top-level contents, incremented per call -- runs a counter that
// the carried entries never advance. A carried prefix followed by that fallback
// therefore hands out delimiters the prefix already used, and two split nodes
// land under one TreeNodeID: silent structural corruption, worse than the
// rejection it was meant to avoid. The fallback on its own is self-consistent
// and is exactly what a change written before the field existed relied on, so
// discard the whole list.
func splitTicketsUnlessUndated(pbTickets []*api.TimeTicket) []*api.TimeTicket {
	for _, pbTicket := range pbTickets {
		if pbTicket == nil {
			return nil
		}
	}

	return pbTickets
}

// withoutEmptyContents removes the content groups holding no tree node.
//
// Like dropUndatedAttrs, this does not reproduce the prior behavior, because
// that behavior was itself a crash: FromTreeNodes reports an absent or empty
// group as a nil root, the nil used to be carried into the operation's content
// slice, and TreeEdit.Execute dereferences each content to deep-copy it — a
// nil-pointer panic on apply, both in the server replaying the change and on
// every replica it is forwarded to. Dropping the group loses nothing a
// well-formed change carried (every group ToTreeNodesWhenEdit writes holds at
// least the root of one content node), and leaves an edit that inserts the
// groups that are intact. All-empty collapses to no content at all, which is
// an ordinary deletion — the same edit the operation already described, since
// the empty group named nothing to insert.
func withoutEmptyContents(pbGroups []*api.TreeNodes) []*api.TreeNodes {
	kept := make([]*api.TreeNodes, 0, len(pbGroups))
	for _, pbGroup := range pbGroups {
		if pbGroup == nil || len(pbGroup.Content) == 0 {
			continue
		}

		kept = append(kept, pbGroup)
	}

	if len(kept) == 0 {
		return nil
	}

	return kept
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
