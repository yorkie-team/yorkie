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
	"bytes"
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ReissueOperations returns copies of the given operations in which every
// ticket issued by the actor `from` names the actor `to` instead. Lamports and
// delimiters are kept, so the order among the tickets is unchanged. A ticket
// with lamport 0 is never re-issued: that is time.InitialTicket, the root
// object's and every sentinel node's identity, shared by all replicas.
//
// It goes through the wire format on purpose: the operations come back exactly
// as the server would decode them, and the walk reaches every TimeTicket the
// protocol carries -- positions, node IDs, split tickets, restore spans and the
// elements nested inside a Set/Add/ArraySet value -- without a per-type list
// that a new field could silently fall out of.
//
// It is only sound when every ticket naming `from` was issued locally and has
// never left this replica, which is the case for a document that has never
// synced. See docs/design/pre-attach-ticket-reissue.md.
func ReissueOperations(
	ops []operations.Operation,
	from, to time.ActorID,
) ([]operations.Operation, error) {
	pbOps, err := ToOperations(ops)
	if err != nil {
		return nil, err
	}

	r := ticketReissuer{from: from.Bytes(), to: to.Bytes()}
	for _, pbOp := range pbOps {
		if err := r.walk(pbOp.ProtoReflect()); err != nil {
			return nil, err
		}
	}

	reissued, err := FromOperations(pbOps)
	if err != nil {
		return nil, err
	}
	if len(reissued) != len(ops) {
		return nil, fmt.Errorf("reissue operations: %d in, %d out", len(ops), len(reissued))
	}

	// JSONElementSimple, the message a Set/Add/ArraySet carries its value in,
	// is lossy for one element type, and a value that lost state would be
	// replayed into the local root as that loss. Re-issue it through the full
	// snapshot encoding instead, which carries every field.
	for i, op := range ops {
		if reissued[i], err = r.reissueLossyValue(op, reissued[i]); err != nil {
			return nil, err
		}
	}
	return reissued, nil
}

// isLossyOnWire reports whether JSONElementSimple -- the message a
// Set/Add/ArraySet carries its value in -- drops state the element holds.
//
//   - Text: the wire carries the Text alone and the Edits that fill it, so a
//     Set/Add/ArraySet that restores a removed Text -- the reverse of a
//     Remove, run by Undo -- loses its content, which a later Edit in the same
//     document may target the nodes of.
//
// Object, Array and Tree are safe: the simple form carries them as the
// marshalled bytes of the very api.JSONElement the snapshot encoding uses.
// Primitive carries its own bytes, and a dedup Counter its HLL registers
// alongside the derived value -- repairing that here would have been
// local-only, since the operation the attach then pushes goes through
// toJSONElementSimple too. Keep this list in step with toJSONElementSimple.
func isLossyOnWire(elem crdt.Element) bool {
	switch elem.(type) {
	case *crdt.Text:
		return true
	}
	return false
}

// reissueLossyValue returns the decoded operation with its value replaced by a
// re-issued copy of the original one, every field included, when the wire form
// of that value is lossy. Any other operation is returned unchanged.
func (r ticketReissuer) reissueLossyValue(
	orig, decoded operations.Operation,
) (operations.Operation, error) {
	var value crdt.Element
	switch o := orig.(type) {
	case *operations.Set:
		value = o.Value()
	case *operations.Add:
		value = o.Value()
	case *operations.ArraySet:
		value = o.Value()
	}
	if value == nil || !isLossyOnWire(value) {
		return decoded, nil
	}

	pbElem, err := toJSONElement(value)
	if err != nil {
		return nil, err
	}
	if err := r.walk(pbElem.ProtoReflect()); err != nil {
		return nil, err
	}
	reissuedValue, err := fromJSONElement(pbElem)
	if err != nil {
		return nil, err
	}

	switch o := decoded.(type) {
	case *operations.Set:
		return operations.NewSet(o.ParentCreatedAt(), o.Key(), reissuedValue, o.ExecutedAt()), nil
	case *operations.Add:
		return operations.NewAdd(o.ParentCreatedAt(), o.PrevCreatedAt(), reissuedValue, o.ExecutedAt()), nil
	case *operations.ArraySet:
		return operations.NewArraySet(o.ParentCreatedAt(), o.CreatedAt(), reissuedValue, o.ExecutedAt()), nil
	}
	return decoded, nil
}

// ticketReissuer rewrites the actor of the TimeTickets in a protobuf message.
type ticketReissuer struct {
	from []byte
	to   []byte
}

// walk rewrites every TimeTicket reachable from the given message in place.
func (r ticketReissuer) walk(m protoreflect.Message) error {
	switch msg := m.Interface().(type) {
	case *api.TimeTicket:
		if msg.Lamport != time.InitialLamport && bytes.Equal(msg.ActorId, r.from) {
			msg.ActorId = bytes.Clone(r.to)
		}
		return nil
	case *api.JSONElementSimple:
		if err := r.walkNestedElement(msg); err != nil {
			return err
		}
	}

	var err error
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsList():
			if fd.Message() == nil {
				return true
			}
			list := v.List()
			for i := 0; i < list.Len() && err == nil; i++ {
				err = r.walk(list.Get(i).Message())
			}
		case fd.IsMap():
			if fd.MapValue().Message() == nil {
				return true
			}
			v.Map().Range(func(_ protoreflect.MapKey, mv protoreflect.Value) bool {
				err = r.walk(mv.Message())
				return err == nil
			})
		case fd.Message() != nil:
			err = r.walk(v.Message())
		}
		return err == nil
	})
	return err
}

// walkNestedElement rewrites the tickets inside the encoded value of an
// Object, Array or Tree element: the wire carries those as the bytes of an
// api.JSONElement, out of reach of the field walk.
func (r ticketReissuer) walkNestedElement(elem *api.JSONElementSimple) error {
	switch elem.Type {
	case api.ValueType_VALUE_TYPE_JSON_OBJECT,
		api.ValueType_VALUE_TYPE_JSON_ARRAY,
		api.ValueType_VALUE_TYPE_TREE:
	default:
		return nil
	}
	if len(elem.Value) == 0 {
		return nil
	}

	nested := &api.JSONElement{}
	if err := proto.Unmarshal(elem.Value, nested); err != nil {
		return fmt.Errorf("unmarshal nested element: %w", err)
	}
	if err := r.walk(nested.ProtoReflect()); err != nil {
		return err
	}
	value, err := marshalOpts.Marshal(nested)
	if err != nil {
		return fmt.Errorf("marshal nested element: %w", err)
	}
	elem.Value = value
	return nil
}
