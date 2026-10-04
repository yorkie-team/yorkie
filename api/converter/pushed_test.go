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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var pushedActor = time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}

func pushedTicket(lamport int64) *time.Ticket {
	return time.NewTicket(lamport, 0, pushedActor)
}

func pushedPrimitive(t *testing.T, lamport int64) *crdt.Primitive {
	t.Helper()
	p, err := crdt.NewPrimitive("v", pushedTicket(lamport))
	require.NoError(t, err)
	return p
}

func validate(t *testing.T, ops ...operations.Operation) error {
	t.Helper()
	pbOps, err := converter.ToOperations(ops)
	require.NoError(t, err)
	return converter.ValidatePushedOperations(pbOps)
}

// TestFromPushedChangePack pins that the function the four push RPCs call
// validates every change of a pack before returning it, and still returns a
// legitimate pack decoded.
func TestFromPushedChangePack(t *testing.T) {
	newPack := func(t *testing.T) *api.ChangePack {
		t.Helper()
		doc := document.New("pushed")
		for _, fn := range []func(r *json.Object){
			func(r *json.Object) { r.SetNewObject("o").SetString("a", "1") },
			func(r *json.Object) { r.SetNewArray("arr").AddNewObject().SetString("k", "v") },
			func(r *json.Object) { r.GetArray("arr").SetInteger(0, 1) },
		} {
			require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
				fn(r)
				return nil
			}))
		}
		pbPack, err := converter.ToChangePack(doc.CreateChangePack())
		require.NoError(t, err)
		require.Len(t, pbPack.Changes, 3)
		return pbPack
	}

	t.Run("a pack a replica built decodes", func(t *testing.T) {
		pack, err := converter.FromPushedChangePack(newPack(t))
		require.NoError(t, err)
		assert.Len(t, pack.Changes, 3)
	})

	t.Run("a crafted operation in any change rejects the pack", func(t *testing.T) {
		pbPack := newPack(t)
		// The last change's ArraySet now claims a value created after it.
		last := pbPack.Changes[2].Operations
		require.Len(t, last, 1)
		arraySet := last[0].GetArraySet()
		require.NotNil(t, arraySet)
		arraySet.Value.CreatedAt = converter.ToTimeTicket(time.NewTicket(time.MaxLamport, 0, pushedActor))

		_, err := converter.FromPushedChangePack(pbPack)
		assert.ErrorIs(t, err, converter.ErrInvalidElementTicket)

		// The lenient reader every other path uses still takes it.
		_, err = converter.FromChangePack(pbPack)
		assert.NoError(t, err)
	})
}

// TestValidatePushedValues pins the rules on the value a Set, Add or ArraySet
// carries. Set's are in TestSetElementRejectsImpossibleTickets.
func TestValidatePushedValues(t *testing.T) {
	arrCreatedAt, prevCreatedAt := pushedTicket(1), pushedTicket(1)

	t.Run("add", func(t *testing.T) {
		assert.NoError(t, validate(t, operations.NewAdd(arrCreatedAt, prevCreatedAt,
			pushedPrimitive(t, 5), pushedTicket(5))), "a fresh value shares its add's ticket")

		assert.ErrorIs(t, validate(t, operations.NewAdd(arrCreatedAt, prevCreatedAt,
			pushedPrimitive(t, 9), pushedTicket(5))), converter.ErrInvalidElementTicket,
			"a value created after its add")

		// Both SDKs copy an array Remove's target before deleting it, and
		// skip the undo when it is already gone, so an Add never restores a
		// tombstone. A removed value would go straight into gcElementPairMap.
		removed := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(5))
		removed.SetRemovedAt(pushedTicket(6))
		assert.ErrorIs(t, validate(t, operations.NewAdd(arrCreatedAt, prevCreatedAt,
			removed, pushedTicket(5))), converter.ErrInvalidElementTicket, "a value that arrives removed")

		// Undo re-identifies the value with the undo's ticket and keeps the
		// copy's older movedAt.
		moved := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(7))
		moved.SetMovedAt(pushedTicket(6))
		assert.NoError(t, validate(t, operations.NewAdd(arrCreatedAt, prevCreatedAt,
			moved, pushedTicket(7))), "a re-identified value keeps its older movedAt")
	})

	t.Run("array set", func(t *testing.T) {
		assert.ErrorIs(t, validate(t, operations.NewArraySet(arrCreatedAt, prevCreatedAt,
			pushedPrimitive(t, 9), pushedTicket(5))), converter.ErrInvalidElementTicket,
			"a value created after its array set")

		// The JS ArraySet reverse copies a displaced value a peer removed,
		// and undo re-identifies it with a newer createdAt.
		reinserted := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(7))
		reinserted.SetRemovedAt(pushedTicket(6))
		assert.NoError(t, validate(t, operations.NewArraySet(arrCreatedAt, prevCreatedAt,
			reinserted, pushedTicket(7))), "a re-identified tombstone the JS reverse sends")
	})

	t.Run("pre-attach value", func(t *testing.T) {
		// Attaching rewrites the operation's executedAt to the client's actor
		// and leaves the value under InitialActorID at the same lamport.
		value, err := crdt.NewPrimitive("v", time.NewTicket(5, 1, time.InitialActorID))
		require.NoError(t, err)
		for _, op := range []operations.Operation{
			operations.NewSet(arrCreatedAt, "k", value, pushedTicket(5).SetActorID(pushedActor)),
			operations.NewAdd(arrCreatedAt, prevCreatedAt, value, time.NewTicket(5, 1, pushedActor)),
			operations.NewArraySet(arrCreatedAt, prevCreatedAt, value, time.NewTicket(5, 1, pushedActor)),
		} {
			assert.NoError(t, validate(t, op))
		}
	})

	t.Run("an object nested in an array is still checked", func(t *testing.T) {
		arr := crdt.NewArray(crdt.NewRGATreeList(), pushedTicket(5))
		obj := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(6))
		member := pushedPrimitive(t, 7)
		obj.Set("a", member)
		member.SetRemovedAt(pushedTicket(7))
		require.NoError(t, arr.Add(obj))

		for _, op := range []operations.Operation{
			operations.NewSet(arrCreatedAt, "k", arr, pushedTicket(9)),
			operations.NewAdd(arrCreatedAt, prevCreatedAt, arr, pushedTicket(5)),
			operations.NewArraySet(arrCreatedAt, prevCreatedAt, arr, pushedTicket(5)),
		} {
			assert.ErrorIs(t, validate(t, op), converter.ErrInvalidElementTicket, "%T", op)
		}
	})
}

// objectPayload returns a Set whose value is an object with one primitive
// member per key, created at the given lamports, and lets edit rewrite the
// encoded members before validation.
func objectPayload(
	t *testing.T,
	members []string,
	edit func(nodes []*api.RHTNode),
) []*api.Operation {
	t.Helper()

	obj := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(2))
	for i, k := range members {
		obj.Set(k, pushedPrimitive(t, int64(3+i)))
	}
	pbOps, err := converter.ToOperations([]operations.Operation{
		operations.NewSet(pushedTicket(1), "k", obj, pushedTicket(9)),
	})
	require.NoError(t, err)

	value := pbOps[0].GetSet().GetValue()
	root := &api.JSONElement{}
	require.NoError(t, proto.Unmarshal(value.Value, root))
	nodes := root.GetJsonObject().GetNodes()
	edit(nodes)
	value.Value, err = proto.Marshal(root)
	require.NoError(t, err)

	return pbOps
}

func primitiveOf(node *api.RHTNode) *api.JSONElement_Primitive {
	return node.GetElement().GetPrimitive()
}

// TestValidatePushedObjectMembers pins the rules that keep every member of a
// pushed object one ElementRHT can hold.
func TestValidatePushedObjectMembers(t *testing.T) {
	byKey := func(nodes []*api.RHTNode, k string) *api.RHTNode {
		for _, n := range nodes {
			if n.Key == k {
				return n
			}
		}
		t.Fatalf("no member %q", k)
		return nil
	}

	t.Run("two members sharing a createdAt", func(t *testing.T) {
		// Decoded, the two collapse into one nodeMapByCreatedAt entry: the
		// first is never seen by a walk of the decoded object, and the copy
		// answers to both keys while the server's snapshot emits one.
		pbOps := objectPayload(t, []string{"a", "b"}, func(nodes []*api.RHTNode) {
			primitiveOf(byKey(nodes, "b")).CreatedAt = primitiveOf(byKey(nodes, "a")).CreatedAt
		})
		assert.ErrorIs(t, converter.ValidatePushedOperations(pbOps), converter.ErrInvalidElementTicket)
	})

	t.Run("a member hidden behind a duplicate is still judged first", func(t *testing.T) {
		// The member that would be collapsed away carries an impossible
		// removedAt; the boundary must not need to see it to refuse.
		pbOps := objectPayload(t, []string{"a", "b"}, func(nodes []*api.RHTNode) {
			a := primitiveOf(byKey(nodes, "a"))
			a.RemovedAt = a.CreatedAt
			primitiveOf(byKey(nodes, "b")).CreatedAt = a.CreatedAt
		})
		assert.Error(t, converter.ValidatePushedOperations(pbOps))
	})

	t.Run("a loser that cannot be tombstoned", func(t *testing.T) {
		// Both members answer to "a". The first is positioned at lamport 4,
		// the second is created at 4 and never moved: it loses, and
		// Element.Remove refuses the winner's ticket because it does not
		// follow the loser's createdAt.
		pbOps := objectPayload(t, []string{"a", "b"}, func(nodes []*api.RHTNode) {
			a, b := byKey(nodes, "a"), byKey(nodes, "b")
			primitiveOf(a).MovedAt = primitiveOf(b).CreatedAt
			b.Key = "a"
		})
		assert.ErrorIs(t, converter.ValidatePushedOperations(pbOps), converter.ErrRefusedMember)
	})

	t.Run("a live loser an older replica left behind", func(t *testing.T) {
		// Before losers were marked removed by their own state, a loser whose
		// occupant was already a tombstone stayed live. Documents still hold
		// that shape, and an undo copies it back; it can be tombstoned, so
		// the boundary takes it.
		pbOps := objectPayload(t, []string{"a", "b"}, func(nodes []*api.RHTNode) {
			b := byKey(nodes, "b")
			b.Key = "a"
			primitiveOf(byKey(nodes, "a")).MovedAt = converter.ToTimeTicket(pushedTicket(8))
		})
		assert.NoError(t, converter.ValidatePushedOperations(pbOps))
	})

	t.Run("a loser a replica emits", func(t *testing.T) {
		// A displaced value under the same key, tombstoned by the Set that
		// replaced it: the encoder writes both, in either order.
		for _, order := range []string{"winner first", "loser first"} {
			pbOps := objectPayload(t, []string{"a", "b"}, func(nodes []*api.RHTNode) {
				a, b := byKey(nodes, "a"), byKey(nodes, "b")
				primitiveOf(a).RemovedAt = primitiveOf(b).CreatedAt
				b.Key = "a"
				if order == "winner first" {
					nodes[0], nodes[1] = nodes[1], nodes[0]
				}
			})
			assert.NoError(t, converter.ValidatePushedOperations(pbOps), order)
		}
	})
}

// TestValidatePushedPayloadIdentities pins that one payload never hands two
// elements the same createdAt, except where undo really does: below the
// elements of one array.
func TestValidatePushedPayloadIdentities(t *testing.T) {
	newObject := func(lamport int64, members map[string]crdt.Element) *crdt.Object {
		obj := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(lamport))
		for k, v := range members {
			obj.Set(k, v)
		}
		return obj
	}
	newArray := func(lamport int64, elems ...crdt.Element) *crdt.Array {
		arr := crdt.NewArray(crdt.NewRGATreeList(), pushedTicket(lamport))
		for _, e := range elems {
			require.NoError(t, arr.Add(e))
		}
		return arr
	}
	set := func(value crdt.Element) operations.Operation {
		return operations.NewSet(pushedTicket(1), "k", value, pushedTicket(20))
	}

	for _, tc := range []struct {
		name  string
		value func() crdt.Element
	}{{
		name: "the value root and one of its members",
		value: func() crdt.Element {
			return newObject(5, map[string]crdt.Element{"a": pushedPrimitive(t, 5)})
		},
	}, {
		name: "members of two sibling objects",
		value: func() crdt.Element {
			return newObject(5, map[string]crdt.Element{
				"x": newObject(6, map[string]crdt.Element{"a": pushedPrimitive(t, 8)}),
				"y": newObject(7, map[string]crdt.Element{"a": pushedPrimitive(t, 8)}),
			})
		},
	}, {
		name: "two elements of one array",
		value: func() crdt.Element {
			return newArray(5, pushedPrimitive(t, 6), pushedPrimitive(t, 6))
		},
	}, {
		name: "an array element and an identity outside the array",
		value: func() crdt.Element {
			return newObject(5, map[string]crdt.Element{
				"arr": newArray(6, newObject(7, map[string]crdt.Element{"a": pushedPrimitive(t, 9)})),
				"b":   pushedPrimitive(t, 9),
			})
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorIs(t, validate(t, set(tc.value())), converter.ErrInvalidElementTicket)
		})
	}

	t.Run("descendants of a restored array element and its tombstone", func(t *testing.T) {
		// Undo restores a removed array element as a copy re-identified at
		// its root only, so the tombstone and the copy share every
		// descendant's createdAt. TestPushBoundaryAcceptsReplicaHistories
		// reaches this shape through Document.Undo/Redo.
		tombstone := newObject(6, map[string]crdt.Element{"a": pushedPrimitive(t, 7)})
		tombstone.SetRemovedAt(pushedTicket(8))
		restored := newObject(9, map[string]crdt.Element{"a": pushedPrimitive(t, 7)})
		assert.NoError(t, validate(t, set(newArray(5, tombstone, restored))))
	})

	t.Run("the document root's identity", func(t *testing.T) {
		// The root lives at time.InitialTicket; no replica issues lamport 0.
		root, err := crdt.NewPrimitive("v", time.InitialTicket)
		require.NoError(t, err)
		assert.ErrorIs(t, validate(t, set(newObject(5, map[string]crdt.Element{"a": root}))),
			converter.ErrInvalidElementTicket, "as a member")
		assert.ErrorIs(t, validate(t, set(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))),
			converter.ErrInvalidElementTicket, "as the value")
	})
}

// TestValidatePushedSetValueRemovedAt pins the Set branch of the value rules.
func TestValidatePushedSetValueRemovedAt(t *testing.T) {
	value := func(removedAt int64) crdt.Element {
		obj := crdt.NewObject(crdt.NewElementRHT(), pushedTicket(5))
		obj.SetRemovedAt(pushedTicket(removedAt))
		return obj
	}

	assert.ErrorIs(t, validate(t, operations.NewSet(pushedTicket(1), "k", value(5), pushedTicket(9))),
		converter.ErrInvalidElementTicket, "removed before it was created")
	// The JS Remove reverse can restore a key's tombstone.
	assert.NoError(t, validate(t, operations.NewSet(pushedTicket(1), "k", value(6), pushedTicket(9))),
		"a tombstone the JS Remove reverse restores")
}

// TestValidatePushedTreeValue pins that a tree value is judged by the tickets
// inside its bytes, which are the ones the decoder keeps.
func TestValidatePushedTreeValue(t *testing.T) {
	newTree := func(lamport int64) *crdt.Tree {
		root := crdt.NewTreeNode(crdt.NewTreeNodeID(pushedTicket(lamport), 0), "doc", nil)
		return crdt.NewTree(root, pushedTicket(lamport))
	}

	t.Run("created after its operation", func(t *testing.T) {
		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewSet(pushedTicket(1), "k", newTree(9), pushedTicket(5)),
		})
		require.NoError(t, err)
		// The simple element's outer ticket is not what the decoder reads.
		pbOps[0].GetSet().Value.CreatedAt = converter.ToTimeTicket(pushedTicket(2))
		assert.ErrorIs(t, converter.ValidatePushedOperations(pbOps), converter.ErrInvalidElementTicket)
	})

	t.Run("an add that arrives removed", func(t *testing.T) {
		tree := newTree(5)
		tree.SetRemovedAt(pushedTicket(6))
		assert.ErrorIs(t, validate(t, operations.NewAdd(pushedTicket(1), pushedTicket(1), tree, pushedTicket(5))),
			converter.ErrInvalidElementTicket)
	})

	t.Run("a fresh tree", func(t *testing.T) {
		assert.NoError(t, validate(t, operations.NewSet(pushedTicket(1), "k", newTree(5), pushedTicket(5))))
	})
}
