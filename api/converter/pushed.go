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
	"fmt"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

// FromPushedChangePack is FromChangePack for a pack a client pushes to the
// server. On top of decoding it, it rejects element payloads no replica can
// produce; see ValidatePushedOperations.
//
// Every other reader -- a client pulling, the server reading a stored change
// or a snapshot -- decodes with FromChangePack and the lenient decoders. They
// hold changes the server already accepted, and data written before a rule
// existed has no other source: failing to read it makes the document
// unloadable, and dropping it makes the reader diverge from every replica
// that applied it.
func FromPushedChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	pack, err := FromChangePack(pbPack)
	if err != nil {
		return nil, err
	}

	for _, pbChange := range pbPack.Changes {
		if err := ValidatePushedOperations(pbChange.Operations); err != nil {
			return nil, err
		}
	}

	return pack, nil
}

// FromLeavingChangePack is FromPushedChangePack for the pack that accompanies
// a client leaving a document -- Detach and Remove. It applies the same rules,
// but a pack that breaks one leaves with its changes dropped instead of
// failing the request.
//
// This is the repair path for the rejection FromPushedChangePack performs, and
// the reason that rejection is survivable at all. The readers are deliberately
// lenient (see above), so a document can already hold a shape the push rules
// refuse -- written before the rule existed, or pulled from a peer that wrote
// it then. A client whose local change carries that shape nested in a Set or
// an undo reverse is rejected on every push, and a client does not discard a
// change a push rejected: it resends the same pack forever. Were Detach and
// Remove to reject it too, that client could not even leave the document to
// get a clean copy, and the rejection would be permanent rather than a
// setback.
//
// Dropping is what leaving can afford and syncing cannot. The changes in this
// pack are the departing client's last local edits; losing them loses an edit,
// while applying them is what the rules exist to prevent, and refusing them is
// what wedges the client. The whole list goes, not the offending change alone:
// later changes in the pack build on the one dropped, and the server has no
// way to rebase them. The detach itself proceeds, the client's checkpoint for
// this document is discarded with its attachment, and re-attaching rebuilds
// the document from the server's snapshot -- which the lenient readers can
// always read.
func FromLeavingChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	pack, err := FromChangePack(pbPack)
	if err != nil {
		return nil, err
	}

	for _, pbChange := range pbPack.Changes {
		if err := ValidatePushedOperations(pbChange.Operations); err != nil {
			pack.Changes = nil
			return pack, nil
		}
	}

	return pack, nil
}

// ValidatePushedOperations rejects an operation whose element payload carries
// tickets no replica could have issued.
//
// createdAt, movedAt and removedAt are decoded from client bytes
// independently of each other and of the operation's executedAt, and nothing
// between the wire and the CRDT makes them agree: Change.SetActor rewrites only
// the operation's own executedAt. The rules below keep an object member from
// ever sitting in an ElementRHT where it can be neither indexed nor
// tombstoned -- live, unreachable by key and charged to Live:
//
//   - An object member's removedAt must follow its createdAt, which is exactly
//     what Element.Remove and DeleteByCreatedAt accept.
//   - An object member's movedAt must not precede its createdAt. ElementRHT
//     anchors both the LWW comparison and the eviction on PositionedAt, so a
//     later Set whose ticket falls between the two would win the key without
//     being able to tombstone the member.
//   - A Set value obeys the removedAt rule, and must not be created after its
//     own Set: the win stamps its movedAt with executedAt, which would
//     position it before its createdAt.
//   - An object member the decoded ElementRHT refuses is an error, not a
//     member to drop; see fromJSONObject.
//
// A false positive here is a setback rather than a dead end: see
// FromLeavingChangePack, which lets a client whose document holds a shape
// these rules refuse detach and re-attach to get a clean copy.
//
// Array elements are exempt from the ticket rules. Undo re-identifies the
// value of an Add or an ArraySet reverse with a freshly issued createdAt
// (Document.executeUndoRedo) while the copy keeps its older movedAt, and the
// JS SDK's ArraySet reverse also keeps an older removedAt, so replicas really
// emit both shapes there and documents already hold them nested in containers.
// The hazard the rules cover for an object member -- a value taking the
// identity of a live element -- is answered for an array element at execution
// time instead, by the guard Add.Execute and ArraySet.Execute share with
// Set.Execute (operations.hijacksLiveElement), which refuses the payload
// rather than the pack and so converges on every replica.
// The JS SDK assigns one ticket to a Set and its value, restores an older value
// under a newer ticket on undo, and never re-identifies an object member, so
// none of the rules above rejects anything a replica sends.
func ValidatePushedOperations(pbOps []*api.Operation) error {
	for _, pbOp := range pbOps {
		if err := validatePushedOperation(pbOp); err != nil {
			return err
		}
	}

	return nil
}

func validatePushedOperation(pbOp *api.Operation) error {
	switch decoded := pbOp.GetBody().(type) {
	case *api.Operation_Set_:
		elem, err := strictElement(decoded.Set.GetValue())
		if err != nil {
			return err
		}
		executedAt, err := fromRequiredTimeTicket(decoded.Set.GetExecutedAt(), "set.executed_at")
		if err != nil {
			return err
		}
		if err := validateRemovedAt(elem); err != nil {
			return err
		}
		if elem.CreatedAt().After(executedAt) {
			return fmt.Errorf("set %s: value created after the set: %w",
				elem.CreatedAt().Key(), ErrInvalidElementTicket)
		}
		return validateObjectMembers(elem)
	case *api.Operation_Add_:
		elem, err := strictElement(decoded.Add.GetValue())
		if err != nil {
			return err
		}
		return validateObjectMembers(elem)
	case *api.Operation_ArraySet_:
		elem, err := strictElement(decoded.ArraySet.GetValue())
		if err != nil {
			return err
		}
		return validateObjectMembers(elem)
	case *api.Operation_Increase_:
		// Increase carries a JSONElementSimple too, and fromElement decodes it
		// as whatever type the payload names -- an Object, an Array, a Text or
		// a Tree are all accepted there. Increase.Execute wants a Primitive,
		// and only a numeric one: Counter.Increase is the only consumer. A
		// replica never sends anything else, so rejecting here costs nothing
		// and keeps a crafted payload from reaching the type switch at all.
		elem, err := fromElement(decoded.Increase.GetValue())
		if err != nil {
			return err
		}
		if _, ok := elem.(*crdt.Primitive); !ok {
			return fmt.Errorf("increase %s: value is not a primitive: %w",
				elem.CreatedAt().Key(), ErrInvalidElementTicket)
		}
		return nil
	default:
		return nil
	}
}

// strictElement decodes an element payload like fromElement, except that an
// object member the ElementRHT refuses is an error.
func strictElement(pbElement *api.JSONElementSimple) (crdt.Element, error) {
	if pbElement != nil && pbElement.Value != nil {
		switch pbElement.Type {
		case api.ValueType_VALUE_TYPE_JSON_OBJECT:
			return bytesToObject(pbElement.Value, false)
		case api.ValueType_VALUE_TYPE_JSON_ARRAY:
			return bytesToArray(pbElement.Value, false)
		}
	}

	return fromElement(pbElement)
}

// validateObjectMembers applies the object-member rules to every object member
// in elem's subtree; see ValidatePushedOperations.
func validateObjectMembers(elem crdt.Element) error {
	container, ok := elem.(crdt.Container)
	if !ok {
		return nil
	}

	var invalid error
	container.Descendants(func(child crdt.Element, parent crdt.Container) bool {
		if _, ok := parent.(*crdt.Object); !ok {
			return false
		}
		if err := validateRemovedAt(child); err != nil {
			invalid = err
		} else if movedAt := child.MovedAt(); movedAt != nil && child.CreatedAt().After(movedAt) {
			invalid = fmt.Errorf("element %s: moved_at precedes created_at: %w",
				child.CreatedAt().Key(), ErrInvalidElementTicket)
		}
		return invalid != nil
	})

	return invalid
}

func validateRemovedAt(elem crdt.Element) error {
	if removedAt := elem.RemovedAt(); removedAt != nil && !removedAt.After(elem.CreatedAt()) {
		return fmt.Errorf("element %s: removed_at does not follow created_at: %w",
			elem.CreatedAt().Key(), ErrInvalidElementTicket)
	}

	return nil
}
