//go:build integration

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestArray(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("causal nested array test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").
				AddString("v1").
				AddNewArray().AddString("1", "2", "3")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/delete simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddString("v1", "v2")
			return nil
		}, "add v1, v2 by c1")
		assert.NoError(t, err)

		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").Delete(1)
			return nil
		}, "delete v2 by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddString("v3")
			return nil
		}, "add v3 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/delete test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddString("v1")
			return nil
		}, "new array and add v1")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddString("v2", "v3")
			root.GetArray("k1").Delete(1)
			return nil
		}, "add v2, v3 and delete v2 by c1")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddString("v4", "v5")
			return nil
		}, "add v4, v5 by c2")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array delete test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddString("v1", "v2", "v3")
			return nil
		}, "new array and add v1 v2 v3")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").Delete(1)
			return nil
		}, "delete v2")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").Delete(1)
			return nil
		}, "delete v2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			assert.Equal(t, 2, root.GetArray("k1").Len())
			return nil
		}, "check array length")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array move test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}, "[0,1,2]")
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			prev := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(prev.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[2,0,1]}`, root.Marshal())
			return nil
		}, "move 2 before 0")
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			prev := root.GetArray("k1").Get(1)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(prev.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[0,2,1]}`, root.Marshal())
			return nil
		}, "move 2 before 1")
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array move with the same position test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddInteger(0, 1, 2)
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			next := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(2)
			root.GetArray("k1").MoveBefore(next.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[2,0,1]}`, root.Marshal())
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			next := root.GetArray("k1").Get(0)
			elem := root.GetArray("k1").Get(1)
			root.GetArray("k1").MoveBefore(next.CreatedAt(), elem.CreatedAt())
			assert.Equal(t, `{"k1":[1,0,2]}`, root.Marshal())
			return nil
		}))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("array.set with value add, delete test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set array with value
			root.SetNewArray("k1", yson.ParseArray("[Int(0),[Int(1),Int(2),Int(3)]]"))
			assert.Equal(t, `{"k1":[0,[1,2,3]]}`, root.Marshal())

			// 02. add value to array
			root.GetArray("k1").AddInteger(4)
			assert.Equal(t, `{"k1":[0,[1,2,3],4]}`, root.Marshal())

			root.GetArray("k1").AddString("str")
			assert.Equal(t, `{"k1":[0,[1,2,3],4,"str"]}`, root.Marshal())

			// 03. delete value from array
			root.GetArray("k1").Delete(3)
			assert.Equal(t, `{"k1":[0,[1,2,3],4]}`, root.Marshal())

			// 04. remove the array and check the number of tombstones.
			root.Delete("k1")
			return nil
		}))

		assert.Equal(t, 8, d1.GarbageLen())
		assert.Equal(t, 8, d1.GarbageCollect(helper.MaxVersionVector(d1.ActorID())))
	})

	t.Run("array.set with value sync test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1", yson.ParseArray(`[Int(0),Int(1),Int(2)]`))
			assert.Equal(t, `{"k1":[0,1,2]}`, root.Marshal())
			return nil
		}))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddInteger(3)
			assert.Equal(t, `{"k1":[0,1,2,3]}`, root.Marshal())
			return nil
		}))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("array.set with Counter, Text, Tree slice test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		// 01. set array with value
		// 02. Edit value in array
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			// Counter
			root.SetNewArray("counters", yson.ParseArray("[Counter(Long(0)),Counter(Long(0)),Counter(Long(0))]"))
			root.GetArray("counters").GetCounter(0).Increase(1)
			assert.Equal(t, `{"counters":[1,0,0]}`, root.Marshal())

			// Text
			root.SetNewArray("texts", yson.ParseArray("[Text(),Text(),Text()]"))
			root.GetArray("texts").GetText(0).Edit(0, 0, "hello")
			assert.Equal(t, `[[{"val":"hello"}],[],[]]`, root.GetArray("texts").Marshal())

			// Tree
			root.SetNewArray("forest", yson.ParseArray("[Tree(),Tree()]"))
			root.GetArray("forest").GetTree(0).Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{},
			}, 0)
			assert.Equal(t, `[{"type":"root","children":[{"type":"p","children":[]}]},{"type":"root","children":[]}]`, root.GetArray("forest").Marshal())
			assert.Equal(t, `<root><p></p></root>`, root.GetArray("forest").GetTree(0).ToXML())
			return nil
		}))
	})
}

func TestArrayConcurrency(t *testing.T) {
	clients := activeClients(t, 2)
	c0, c1 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	initArr := []int{1, 2, 3, 4}
	initMarshal := `[1,2,3,4]`
	oneIdx := 1
	otherIdxs := []int{2, 3}
	newValues := []int{5, 6}

	type arrayOp struct {
		opName   string
		executor func(*json.Array, int)
	}

	// NOTE(junseo): It tests all (op1, op2) pairs in operations.
	// `oneIdx` is the index where both op1 and op2 reference.
	// `opName` represents the parameter of operation selected as `oneIdx'.
	// `otherIdxs` ensures that indexs other than `oneIdx` are not duplicated.
	operations := []arrayOp{
		// insert
		{"insert.prev", func(a *json.Array, cid int) {
			a.InsertIntegerAfter(oneIdx, newValues[cid])
		}},
		{"insert.prev.next", func(a *json.Array, cid int) {
			a.InsertIntegerAfter(oneIdx-1, newValues[cid])
		}},

		// move
		{"move.prev", func(a *json.Array, cid int) {
			a.MoveAfterByIndex(oneIdx, otherIdxs[cid])
		}},
		{"move.prev.next", func(a *json.Array, cid int) {
			a.MoveAfterByIndex(oneIdx-1, otherIdxs[cid])
		}},
		{"move.target", func(a *json.Array, cid int) {
			a.MoveAfterByIndex(otherIdxs[cid], oneIdx)
		}},

		// set by index
		{"set.target", func(a *json.Array, cid int) {
			a.SetInteger(oneIdx, newValues[cid])
		}},

		// remove
		{"remove.target", func(a *json.Array, cid int) {
			a.Delete(oneIdx)
		}},
	}

	ctx := context.Background()
	d0 := document.New(helper.TestDocKey(t))
	assert.NoError(t, c0.Attach(ctx, d0))
	d1 := document.New(helper.TestDocKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))

	for _, op1 := range operations {
		for _, op2 := range operations {
			t.Run(op1.opName+" vs "+op2.opName, func(t *testing.T) {
				assert.NoError(t, d0.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewArray("a").AddInteger(initArr...)
					assert.Equal(t, initMarshal, root.GetArray("a").Marshal())
					return nil
				}))

				assert.NoError(t, c0.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d0.Update(func(root *json.Object, p *presence.Presence) error {
					op1.executor(root.GetArray("a"), 0)
					return nil
				}))

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					op2.executor(root.GetArray("a"), 1)
					return nil
				}))

				syncClientsThenAssertEqual(t, []clientAndDocPair{{c0, d0}, {c1, d1}})
			})
		}
	}
}

func TestComplicatedArrayConcurrency(t *testing.T) {
	clients := activeClients(t, 3)
	c0, c1 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	initArr := []int{1, 2, 3, 4}
	initMarshal := `[1,2,3,4]`
	oneIdx := 1
	otherIdx := 0
	newValue := 5

	type arrayOp struct {
		opName   string
		executor func(*json.Array)
	}

	// This test checks CRDT convergence in the presence of concurrent modifications:
	// - Client 0 performs a single operation (`op`) at index `oneIdx`.
	// - Client 1 performs two move operations involving index `oneIdx`.
	// The test ensures that after syncing both clients, their array states converge.
	// `oneIdx`: the index on which both the arbitrary operation and the first move operation are applied.
	// `opName`: describes the type of operation being tested (insert, move, set, or remove).
	operations := []arrayOp{
		// insert
		{"insert", func(a *json.Array) { a.InsertIntegerAfter(oneIdx, newValue) }},

		// move
		{"move", func(a *json.Array) { a.MoveAfterByIndex(otherIdx, oneIdx) }},

		// set
		{"set", func(a *json.Array) { a.SetInteger(oneIdx, newValue) }},

		// remove
		{"remove", func(a *json.Array) { a.Delete(oneIdx) }},
	}

	ctx := context.Background()
	d0 := document.New(helper.TestDocKey(t))
	assert.NoError(t, c0.Attach(ctx, d0))
	d1 := document.New(helper.TestDocKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))

	for _, op := range operations {
		t.Run(op.opName, func(t *testing.T) {
			assert.NoError(t, d0.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewArray("a").AddInteger(initArr...)
				assert.Equal(t, initMarshal, root.GetArray("a").Marshal())
				return nil
			}))

			assert.NoError(t, c0.Sync(ctx))
			assert.NoError(t, c1.Sync(ctx))

			assert.NoError(t, d0.Update(func(root *json.Object, p *presence.Presence) error {
				op.executor(root.GetArray("a"))
				return nil
			}))

			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetArray("a").MoveAfterByIndex(2, oneIdx)
				root.GetArray("a").MoveAfterByIndex(3, 2)
				return nil
			}))

			if !syncClientsThenCheckEqual(t, []clientAndDocPair{{c0, d0}, {c1, d1}}) {
				t.Skipf("convergence failed for operation %s", op.opName)
			}
		})
	}
}

func TestArraySetByIndex(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("array set simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddInteger(-1, -2, -3)
			assert.Equal(t, `{"k1":[-1,-2,-3]}`, root.Marshal())
			return nil
		}, "add -1, -2, -3 by c1"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").SetInteger(1, -4)
			assert.Equal(t, `{"k1":[-1,-4,-3]}`, root.Marshal())
			return nil
		}, "set k1[1] to -4 by c2"))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").SetInteger(0, -5)
			assert.Equal(t, `{"k1":[-5,-2,-3]}`, root.Marshal())
			return nil
		}, "set k1[0] to -5 by c1"))

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
