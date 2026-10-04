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
//   - An ArraySet value's removedAt, if any, must precede its createdAt. The
//     JS SDK's ArraySet reverse copies a displaced value a peer already
//     removed, so that value can arrive removed -- but it only ever reaches a
//     push through undo, which re-identifies the copy with the undo's own
//     fresh ticket (executeUndoRedo's ArraySet branch, document.go and
//     document.ts). A tombstone arriving under a createdAt no older than its
//     removal is a shape no replica emits, and it reaches the same
//     RegisterElement -> gcElementPairMap sink the Add rule guards.
//
// Every object member nested in that value:
//
//   - removedAt must follow createdAt, as above.
//   - movedAt must not precede createdAt. ElementRHT anchors both the LWW
//     comparison and the eviction on PositionedAt, so a later Set whose ticket
//     falls between the two would win the key without being able to
//     tombstone the member.
//   - A member that loses its key to one decoded before it must be removed or
//     removable: either it carries a removedAt, or the winner's positionedAt
//     follows its createdAt. Otherwise ElementRHT can neither tombstone nor
//     index it by key, and it stays live, unreachable and charged to Live.
//
// These are safe for undo copies of existing documents too, since a copy
// carries what the document held. No replica can have built a member that
// breaks them: Element.Remove refuses a removedAt that does not follow
// createdAt in both SDKs (and has since 2022), ElementRHT stamps a winning
// member's movedAt with an executedAt no older than the value, and a live
// loser left by older replicas can still be tombstoned, which is all the
// loser rule asks.
//
// Identities across the whole value (see payloadIDs and validateMembers):
//
//   - No element of the value -- its root, an object member at any depth, an
//     array element -- reuses a createdAt another one claimed. Root registers
//     every one of them in one document-wide map keyed by createdAt. Within
//     one object the encoder cannot even emit two (Nodes() reads ElementRHT's
//     createdAt index, in both SDKs), and two of them would collapse on
//     decode. The one exception is between the descendants of two elements of
//     one array, where an undo-restored copy and its tombstone share their
//     descendants. An array element's own createdAt is never exempt, not even
//     against another element's descendants.
//   - No element is created at lamport 0, which no replica issues and which
//     the document root's time.InitialTicket carries.
//
// A tree value is read from its bytes like a container, because that is where
// the decoder takes its tickets from.
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

	// removedAtPrecedesCreatedAt accepts a removed value only when it was
	// removed before the createdAt it arrives under (ArraySet).
	removedAtPrecedesCreatedAt
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
			removedAtPrecedesCreatedAt)
	default:
		return nil
	}
}

// validateValue applies the value rules to the value of one operation, then
// the payload-wide identity rules and the member rules to its subtree.
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
	case removedAtPrecedesCreatedAt:
		if tickets.removedAt != nil && !tickets.createdAt.After(tickets.removedAt) {
			return fmt.Errorf("%s %s: value arrives removed at or after its own created_at: %w",
				op, tickets.createdAt.Key(), ErrInvalidElementTicket)
		}
	}

	ids := newPayloadIDs(nil)
	if err := ids.claim(tickets.createdAt); err != nil {
		return err
	}
	if root == nil {
		return nil
	}
	return validateMembers(root, ids)
}

// valueTickets reads the tickets of an operation's value from the same bytes
// fromElement builds it from. A container that carries its subtree, and a
// tree, are decoded from those bytes, and the subtree is returned to walk;
// anything else keeps only the createdAt of the simple element, which is all
// fromElement reads from it.
func valueTickets(op string, pbValue *api.JSONElementSimple) (*api.JSONElement, elementTickets, error) {
	if pbValue == nil {
		return nil, elementTickets{}, fmt.Errorf("%s.value: %w", op, ErrUnsupportedElement)
	}

	fromBytes := false
	switch pbValue.GetType() {
	case api.ValueType_VALUE_TYPE_JSON_OBJECT, api.ValueType_VALUE_TYPE_JSON_ARRAY:
		fromBytes = pbValue.GetValue() != nil
	case api.ValueType_VALUE_TYPE_TREE:
		fromBytes = true
	}

	if fromBytes {
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

	createdAt, err := fromRequiredTimeTicket(pbValue.GetCreatedAt(), op+".value.created_at")
	if err != nil {
		return nil, elementTickets{}, err
	}
	return nil, elementTickets{createdAt: createdAt}, nil
}

// payloadIDs is the set of element identities one operation's value has
// claimed so far, from its root down through every object member and array
// element. A scope opened for one element of an array sees what its parent
// claimed but not what the array's other elements claimed; see
// validateMembers.
type payloadIDs struct {
	parent *payloadIDs
	ids    map[string]struct{}
}

func newPayloadIDs(parent *payloadIDs) *payloadIDs {
	return &payloadIDs{parent: parent, ids: map[string]struct{}{}}
}

func (s *payloadIDs) has(key string) bool {
	for scope := s; scope != nil; scope = scope.parent {
		if _, ok := scope.ids[key]; ok {
			return true
		}
	}
	return false
}

// claim records createdAt as an identity of this payload. It refuses one the
// payload already used: every element of a value is registered into
// Root.elementMap, a document-wide map keyed by createdAt, so two of them
// under one ticket leave one unaddressable -- never removable, charged to
// Live, re-emitted into every snapshot -- and collapse into one
// gcElementPairMap entry once both are removed.
//
// It also refuses lamport 0. No replica issues such a ticket -- a client's
// first change is lamport 1 -- and the document root lives at
// time.InitialTicket, so a value claiming it would take over the root's
// elementMap slot and capture every later root-level operation.
func (s *payloadIDs) claim(createdAt *time.Ticket) error {
	if createdAt.Lamport() == 0 {
		return fmt.Errorf("element %s: lamport 0 is never issued: %w",
			createdAt.Key(), ErrInvalidElementTicket)
	}

	key := createdAt.Key()
	if s.has(key) {
		return fmt.Errorf("element %s: created_at reused within one payload: %w",
			key, ErrInvalidElementTicket)
	}
	s.ids[key] = struct{}{}
	return nil
}

// validateMembers claims the identity of every element in elem's subtree and
// applies the member rules to every object in it; see
// ValidatePushedOperations. Text and tree content keep their own node
// identities, which are not elements and are not walked.
//
// The elements of one array are judged apart from each other below their own
// level. Undo restores a removed array element as a deep copy re-identified
// at its root only (Document.executeUndoRedo, document.ts), so the array then
// holds the tombstone and the live copy, and their descendants share every
// createdAt; two replicas undoing concurrent removals of one element leave
// two live copies the same way. Both SDKs emit that array whole whenever an
// enclosing value is copied. The elements' own createdAts stay unique -- the
// re-identification is there to keep them so -- and every identity in the
// array is still distinct from everything outside it, and from every other
// identity the array holds: an element's own createdAt is checked against the
// descendants of its siblings as well as against their roots, in both decode
// orders, so the exemption covers descendant-to-descendant collisions only.
func validateMembers(elem *api.JSONElement, ids *payloadIDs) error {
	switch body := elem.GetBody().(type) {
	case *api.JSONElement_JsonObject:
		return validateObjectMembers(body.JsonObject, ids)
	case *api.JSONElement_JsonArray:
		tops := map[string]struct{}{}
		below := map[string]struct{}{}
		for _, pbNode := range body.JsonArray.GetNodes() {
			if pbNode.GetElement() == nil {
				// A dead position left by a move; it is not an element.
				continue
			}
			tickets, err := ticketsOf(pbNode.GetElement())
			if err != nil {
				return err
			}

			key := tickets.createdAt.Key()
			if _, ok := tops[key]; ok {
				return fmt.Errorf("element %s: created_at reused within one array: %w",
					key, ErrInvalidElementTicket)
			}
			// An element's own identity is not covered by the exemption: a
			// restored copy is re-identified precisely so it differs from
			// everything the array already holds, descendants included. The
			// check runs in both directions -- here against the descendants of
			// the elements decoded before this one, and below against the
			// elements decoded before this one's descendants -- since the
			// decode order of the colliding pair is the sender's to choose.
			if _, ok := below[key]; ok {
				return fmt.Errorf("element %s: created_at reused below another element of one array: %w",
					key, ErrInvalidElementTicket)
			}

			elemIDs := newPayloadIDs(ids)
			if err := elemIDs.claim(tickets.createdAt); err != nil {
				return err
			}
			if err := validateMembers(pbNode.GetElement(), elemIDs); err != nil {
				return err
			}

			tops[key] = struct{}{}
			for id := range elemIDs.ids {
				if id == key {
					continue
				}
				if _, ok := tops[id]; ok {
					return fmt.Errorf("element %s: created_at reused below another element of one array: %w",
						id, ErrInvalidElementTicket)
				}
				below[id] = struct{}{}
			}
		}
		for key := range tops {
			ids.ids[key] = struct{}{}
		}
		for key := range below {
			ids.ids[key] = struct{}{}
		}
	}

	return nil
}

// validateObjectMembers replays the members of one object in the order
// fromJSONObject feeds them to ElementRHT.SetWithExecutedAt, and rejects a
// member that hashtable could not hold.
func validateObjectMembers(pbObj *api.JSONElement_JSONObject, ids *payloadIDs) error {
	positionedAts := make(map[string]*time.Ticket, len(pbObj.GetNodes()))

	for _, pbNode := range pbObj.GetNodes() {
		if pbNode.GetElement() == nil {
			return fmt.Errorf("json_object.node %q: %w", pbNode.GetKey(), ErrUnsupportedElement)
		}
		tickets, err := ticketsOf(pbNode.GetElement())
		if err != nil {
			return err
		}
		if err := ids.claim(tickets.createdAt); err != nil {
			return err
		}

		key := tickets.createdAt.Key()
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

		if err := validateMembers(pbNode.GetElement(), ids); err != nil {
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
