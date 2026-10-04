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

	"google.golang.org/protobuf/proto"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
//
// See docs/design/pushed-payload-validation.md.
func FromPushedChangePack(pbPack *api.ChangePack) (*change.Pack, error) {
	for _, pbChange := range pbPack.GetChanges() {
		if err := ValidatePushedOperations(pbChange.GetOperations()); err != nil {
			return nil, err
		}
	}

	return FromChangePack(pbPack)
}

// ValidatePushedOperations rejects an operation whose element payload is a
// shape no replica can produce. It is stateless: it reads the operation's
// own bytes and nothing of the document it targets, so it never fires
// differently on two servers, and it judges nothing a legitimate history can
// emit. That second property is the one every rule below is held to; a rule
// that a real client can trip wedges that client, because a rejected change
// stays at the head of its push queue.
//
// createdAt, movedAt and removedAt are decoded from client bytes
// independently of each other and of the operation's executedAt, and nothing
// between the wire and the CRDT makes them agree: Change.SetActor rewrites only
// the operation's own executedAt.
//
// The value an operation carries (Set, Add, ArraySet):
//
//   - It must not be created after its own operation. Both SDKs issue one
//     ticket for a fresh value and its operation, and undo either restores an
//     older value (Set) or re-identifies it with the undo's own ticket (Add,
//     ArraySet; Document.executeUndoRedo). A value that pre-dates attach keeps
//     InitialActorID, which orders before every real actor at the same
//     lamport. A Set value created later than its Set would be positioned
//     before its own createdAt once it wins.
//   - A Set value's removedAt must follow its createdAt, which is what
//     Element.Remove and DeleteByCreatedAt accept. The JS Remove reverse can
//     restore a key's tombstone, so a removed Set value is legitimate; one
//     removed before it existed is not.
//   - An Add value carries no removedAt. Both SDKs build the Add that undoes
//     an array Remove from the target before deleting it, skip it when the
//     target is already gone, and add only live values otherwise.
//   - An ArraySet value's removedAt is not judged: the JS SDK's ArraySet
//     reverse copies a displaced value a peer already removed, and undo then
//     re-identifies that copy with a newer createdAt.
//
// Every object member nested in that value:
//
//   - removedAt must follow createdAt, as above.
//   - movedAt must not precede createdAt. ElementRHT anchors both the LWW
//     comparison and the eviction on PositionedAt, so a later Set whose ticket
//     falls between the two would win the key without being able to
//     tombstone the member.
//   - No two members of one object share a createdAt. ElementRHT keys its
//     second index by createdAt, so the encoder emits one node per createdAt
//     (Nodes() reads that index, in both SDKs). Two of them collapse into one
//     entry on decode: one is never validated, and the decoded copy answers
//     to both keys while the server's snapshot carries one.
//   - A member that loses its key to one decoded before it must be removed or
//     removable: either it carries a removedAt, or the winner's positionedAt
//     follows its createdAt. Otherwise ElementRHT can neither tombstone nor
//     index it by key, and it stays live, unreachable and charged to Live.
//
// Array elements nested in the value are exempt from the ticket rules. Undo
// re-identifies an Add or ArraySet value with a fresh createdAt while the copy
// keeps its older movedAt, and the JS ArraySet reverse also keeps an older
// removedAt, so documents already hold both shapes inside arrays. An object
// nested inside an array is still checked.
//
// What the validator cannot see is a createdAt that collides with an element
// already in the document. Telling that apart needs the document, and on main
// today legitimate histories produce it: elements created before attach share
// the initial actor's tickets across clients, and two replicas undoing
// concurrent overwrites restore one value under one createdAt. A replicated
// refusal keyed on that collision fires on those histories, in Go only, and
// splits the Go replicas and the server's snapshot replay from the JS SDK.
func ValidatePushedOperations(pbOps []*api.Operation) error {
	for _, pbOp := range pbOps {
		if err := validatePushedOperation(pbOp); err != nil {
			return err
		}
	}

	return nil
}

// valueRule is what ValidatePushedOperations asks of the removedAt of the
// value an operation carries.
type valueRule int

const (
	// removedAtFollowsCreatedAt accepts a removed value whose removedAt follows
	// its createdAt (Set).
	removedAtFollowsCreatedAt valueRule = iota

	// removedAtAbsent accepts only a live value (Add).
	removedAtAbsent

	// removedAtUnjudged leaves removedAt alone (ArraySet).
	removedAtUnjudged
)

func validatePushedOperation(pbOp *api.Operation) error {
	switch decoded := pbOp.GetBody().(type) {
	case *api.Operation_Set_:
		return validateValue("set", decoded.Set.GetValue(), decoded.Set.GetExecutedAt(),
			removedAtFollowsCreatedAt)
	case *api.Operation_Add_:
		return validateValue("add", decoded.Add.GetValue(), decoded.Add.GetExecutedAt(),
			removedAtAbsent)
	case *api.Operation_ArraySet_:
		return validateValue("array_set", decoded.ArraySet.GetValue(), decoded.ArraySet.GetExecutedAt(),
			removedAtUnjudged)
	default:
		return nil
	}
}

// validateValue applies the value rules to the value of one operation, then
// the member rules to every object nested in it.
func validateValue(op string, pbValue *api.JSONElementSimple, pbExecutedAt *api.TimeTicket, rule valueRule) error {
	executedAt, err := fromRequiredTimeTicket(pbExecutedAt, op+".executed_at")
	if err != nil {
		return err
	}

	root, tickets, err := valueTickets(op, pbValue)
	if err != nil {
		return err
	}

	if tickets.createdAt.After(executedAt) {
		return fmt.Errorf("%s %s: value created after the operation: %w",
			op, tickets.createdAt.Key(), ErrInvalidElementTicket)
	}
	switch rule {
	case removedAtFollowsCreatedAt:
		if err := tickets.validateRemovedAt(); err != nil {
			return err
		}
	case removedAtAbsent:
		if tickets.removedAt != nil {
			return fmt.Errorf("%s %s: value arrives removed: %w",
				op, tickets.createdAt.Key(), ErrInvalidElementTicket)
		}
	case removedAtUnjudged:
	}

	if root == nil {
		return nil
	}
	return validateMembers(root)
}

// valueTickets reads the tickets of an operation's value from the same bytes
// fromElement builds it from: a container that carries its subtree is decoded
// from the subtree, and it then also returns that subtree to walk; anything
// else keeps only the createdAt of the simple element.
func valueTickets(op string, pbValue *api.JSONElementSimple) (*api.JSONElement, elementTickets, error) {
	if pbValue == nil {
		return nil, elementTickets{}, fmt.Errorf("%s.value: %w", op, ErrUnsupportedElement)
	}

	switch pbValue.GetType() {
	case api.ValueType_VALUE_TYPE_JSON_OBJECT, api.ValueType_VALUE_TYPE_JSON_ARRAY:
		if pbValue.GetValue() != nil {
			root := &api.JSONElement{}
			if err := proto.Unmarshal(pbValue.GetValue(), root); err != nil {
				return nil, elementTickets{}, fmt.Errorf("%s.value: unmarshal element: %w", op, err)
			}
			tickets, err := ticketsOf(root)
			if err != nil {
				return nil, elementTickets{}, err
			}
			return root, tickets, nil
		}
	}

	createdAt, err := fromRequiredTimeTicket(pbValue.GetCreatedAt(), op+".value.created_at")
	if err != nil {
		return nil, elementTickets{}, err
	}
	return nil, elementTickets{createdAt: createdAt}, nil
}

// validateMembers applies the member rules to every object in elem's subtree;
// see ValidatePushedOperations.
func validateMembers(elem *api.JSONElement) error {
	switch body := elem.GetBody().(type) {
	case *api.JSONElement_JsonObject:
		return validateObjectMembers(body.JsonObject)
	case *api.JSONElement_JsonArray:
		for _, pbNode := range body.JsonArray.GetNodes() {
			if pbNode.GetElement() == nil {
				continue
			}
			if err := validateMembers(pbNode.GetElement()); err != nil {
				return err
			}
		}
	}

	return nil
}

// validateObjectMembers replays the members of one object in the order
// fromJSONObject feeds them to ElementRHT.SetWithExecutedAt, and rejects a
// member that hashtable could not hold.
func validateObjectMembers(pbObj *api.JSONElement_JSONObject) error {
	createdAts := make(map[string]struct{}, len(pbObj.GetNodes()))
	positionedAts := make(map[string]*time.Ticket, len(pbObj.GetNodes()))

	for _, pbNode := range pbObj.GetNodes() {
		if pbNode.GetElement() == nil {
			return fmt.Errorf("json_object.node %q: %w", pbNode.GetKey(), ErrUnsupportedElement)
		}
		tickets, err := ticketsOf(pbNode.GetElement())
		if err != nil {
			return err
		}

		key := tickets.createdAt.Key()
		if _, ok := createdAts[key]; ok {
			return fmt.Errorf("json_object.node %q: created_at %s already taken: %w",
				pbNode.GetKey(), key, ErrRefusedMember)
		}
		createdAts[key] = struct{}{}

		if err := tickets.validateRemovedAt(); err != nil {
			return err
		}
		if tickets.movedAt != nil && tickets.createdAt.After(tickets.movedAt) {
			return fmt.Errorf("element %s: moved_at precedes created_at: %w",
				key, ErrInvalidElementTicket)
		}

		positionedAt := tickets.positionedAt()
		occupant, ok := positionedAts[pbNode.GetKey()]
		if !ok || positionedAt.After(occupant) {
			positionedAts[pbNode.GetKey()] = positionedAt
		} else if tickets.removedAt == nil && !occupant.After(tickets.createdAt) {
			return fmt.Errorf("json_object.node %q: loser %s cannot be tombstoned: %w",
				pbNode.GetKey(), key, ErrRefusedMember)
		}

		if err := validateMembers(pbNode.GetElement()); err != nil {
			return err
		}
	}

	return nil
}

// elementTickets is the ticket triple of one element payload.
type elementTickets struct {
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

func (t elementTickets) positionedAt() *time.Ticket {
	if t.movedAt != nil {
		return t.movedAt
	}
	return t.createdAt
}

func (t elementTickets) validateRemovedAt() error {
	if t.removedAt != nil && !t.removedAt.After(t.createdAt) {
		return fmt.Errorf("element %s: removed_at does not follow created_at: %w",
			t.createdAt.Key(), ErrInvalidElementTicket)
	}

	return nil
}

// ticketsOf reads the ticket triple of any element body.
func ticketsOf(elem *api.JSONElement) (elementTickets, error) {
	var created, moved, removed *api.TimeTicket
	switch body := elem.GetBody().(type) {
	case *api.JSONElement_JsonObject:
		created, moved, removed = body.JsonObject.GetCreatedAt(), body.JsonObject.GetMovedAt(),
			body.JsonObject.GetRemovedAt()
	case *api.JSONElement_JsonArray:
		created, moved, removed = body.JsonArray.GetCreatedAt(), body.JsonArray.GetMovedAt(),
			body.JsonArray.GetRemovedAt()
	case *api.JSONElement_Primitive_:
		created, moved, removed = body.Primitive.GetCreatedAt(), body.Primitive.GetMovedAt(),
			body.Primitive.GetRemovedAt()
	case *api.JSONElement_Text_:
		created, moved, removed = body.Text.GetCreatedAt(), body.Text.GetMovedAt(), body.Text.GetRemovedAt()
	case *api.JSONElement_Counter_:
		created, moved, removed = body.Counter.GetCreatedAt(), body.Counter.GetMovedAt(),
			body.Counter.GetRemovedAt()
	case *api.JSONElement_Tree_:
		created, moved, removed = body.Tree.GetCreatedAt(), body.Tree.GetMovedAt(), body.Tree.GetRemovedAt()
	default:
		return elementTickets{}, ErrUnsupportedElement
	}

	var t elementTickets
	var err error
	if t.createdAt, err = fromRequiredTimeTicket(created, "element.created_at"); err != nil {
		return elementTickets{}, err
	}
	if t.movedAt, err = fromTimeTicket(moved); err != nil {
		return elementTickets{}, err
	}
	if t.removedAt, err = fromTimeTicket(removed); err != nil {
		return elementTickets{}, err
	}
	return t, nil
}
