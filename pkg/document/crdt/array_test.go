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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestArray(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		treeList := crdt.NewRGATreeList()
		a := crdt.NewArray(treeList, ctx.IssueTimeTicket())

		primitive, err := crdt.NewPrimitive("1", ctx.IssueTimeTicket())
		assert.NoError(t, err)
		err = a.Add(primitive)
		assert.NoError(t, err)

		assert.NoError(t, err)
		assert.Equal(t, `["1"]`, a.Marshal())

		primitive, err = crdt.NewPrimitive("2", ctx.IssueTimeTicket())
		assert.NoError(t, err)
		err = a.Add(primitive)
		assert.NoError(t, err)

		assert.NoError(t, err)
		assert.Equal(t, `["1","2"]`, a.Marshal())

		primitive, err = crdt.NewPrimitive("3", ctx.IssueTimeTicket())
		assert.NoError(t, err)
		err = a.Add(primitive)
		assert.NoError(t, err)

		assert.NoError(t, err)
		assert.Equal(t, `["1","2","3"]`, a.Marshal())
	})

	t.Run("should handle delete operations using document", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("k1")
			arr.AddString("1", "2", "3")
			assert.Equal(t, `{"k1":["1","2","3"]}`, root.Marshal())
			return nil
		}, "initialize array"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("k1")
			_ = arr.Delete(1)
			arr.AddString("4")
			assert.Equal(t, `{"k1":["1","3","4"]}`, root.Marshal())
			return nil
		}, "delete '2' and add '4'"))
	})

	t.Run("can push array element after delete operation (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: ["1", "2", "3"]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("k1")
			arr.AddString("1", "2", "3")
			assert.Equal(t, `{"k1":["1","2","3"]}`, root.Marshal())
			return nil
		}))

		// Step 2: delete "2", then push "4"
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("k1")
			_ = arr.Delete(1)
			arr.AddString("4")
			return nil
		}))

		// Step 3: push nested array [4,5,6]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			subArr := root.GetArray("k1").AddNewArray()
			subArr.AddInteger(4, 5, 6)
			assert.Equal(t, `{"k1":["1","3","4",[4,5,6]]}`, root.Marshal())
			return nil
		}))

		// Final check
		assert.Equal(t, `{"k1":["1","3","4",[4,5,6]]}`, doc.Marshal())
	})

	t.Run("can push object element after delete operation", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: ["1", "2", "3"]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("k1")
			arr.AddString("1", "2", "3")
			assert.Equal(t, `{"k1":["1","2","3"]}`, root.Marshal())
			return nil
		}, "set [1,2,3]"))

		// Step 2: delete "2", then push "4"
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("k1")
			_ = arr.Delete(1)
			arr.AddString("4")
			return nil
		}, "delete '2' and push '4'"))

		// Step 3: push object {"a":"a", "b":"b"}
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			obj := root.GetArray("k1").AddNewObject()
			obj.SetString("a", "a")
			obj.SetString("b", "b")
			assert.Equal(t, `{"k1":["1","3","4",{"a":"a","b":"b"}]}`, root.Marshal())
			return nil
		}, "push object"))

		assert.Equal(t, `{"k1":["1","3","4",{"a":"a","b":"b"}]}`, doc.Marshal())
	})

	t.Run("can push array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: [1,2,3]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("arr")
			arr.AddInteger(1, 2, 3)
			return nil
		}))

		// Step 2: push nested array [4,5,6]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			sub := root.GetArray("arr").AddNewArray()
			sub.AddInteger(4, 5, 6)
			assert.Equal(t, `{"arr":[1,2,3,[4,5,6]]}`, root.Marshal())
			return nil
		}))

		assert.Equal(t, `{"arr":[1,2,3,[4,5,6]]}`, doc.Marshal())
	})

	t.Run("can push element then delete it by ID in array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: [4,3,2,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(4, 3, 2, 1)
			assert.Equal(t, `{"list":[4,3,2,1]}`, root.Marshal())
			return nil
		}))

		// Step 2: delete "2"
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			_ = arr.Delete(2)
			assert.Equal(t, `{"list":[4,3,1]}`, root.Marshal())
			return nil
		}))

		// Step 3: push 2
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.AddInteger(2)
			assert.Equal(t, `{"list":[4,3,1,2]}`, root.Marshal())
			return nil
		}))

		assert.Equal(t, `{"list":[4,3,1,2]}`, doc.Marshal())
	})

	t.Run("can insert an element after the given element in array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: [1,2,4]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(1, 2, 4)
			assert.Equal(t, `{"list":[1,2,4]}`, root.Marshal())
			return nil
		}))

		// Step 2: push 3 after 1 => [1,2,3,4]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.InsertIntegerAfter(1, 3)
			assert.Equal(t, `{"list":[1,2,3,4]}`, root.Marshal())
			return nil
		}))

		// Step 3: delete "2" => [1,3,4]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.Delete(1)
			assert.Equal(t, `{"list":[1,3,4]}`, root.Marshal())
			return nil
		}))

		// Step 4: push 2 after index 0 (1) => [1,2,3,4]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.InsertIntegerAfter(0, 2)
			assert.Equal(t, `{"list":[1,2,3,4]}`, root.Marshal())

			for i := 0; i < arr.Len(); i++ {
				elem := arr.Get(i)
				prim := elem.(*crdt.Primitive).Value()
				assert.Equal(t, int32(i+1), prim)
			}
			return nil
		}))
	})

	t.Run("can move an element before the given element in array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		var prevID, targetID *time.Ticket

		// Step 1: [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(0, 1, 2)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "initialize"))

		// Step 2: Move 2 before 0 => [2,0,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			prev := arr.Get(0)
			target := arr.Get(2)
			prevID = prev.CreatedAt()
			targetID = target.CreatedAt()
			arr.MoveBefore(prevID, targetID)
			assert.Equal(t, `{"list":[2,0,1]}`, root.Marshal())
			return nil
		}, "move 2 before 0"))

		// Step 3: Move 1 before 2 => [1,2,0]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			prev := arr.Get(0)   // 2
			target := arr.Get(2) // 1
			prevID = prev.CreatedAt()
			targetID = target.CreatedAt()
			arr.MoveBefore(prevID, targetID)
			assert.Equal(t, `{"list":[1,2,0]}`, root.Marshal())
			return nil
		}, "move 1 before 2"))
	})

	t.Run("can move an element after the given element in array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))

		// Step 1: [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(0, 1, 2)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "initialize"))

		// Step 2: Move 2 after 0 => [0,2,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.MoveAfterByIndex(0, 2)
			assert.Equal(t, `{"list":[0,2,1]}`, root.Marshal())
			return nil
		}, "move 2 after 0"))

		// Step 3: Move 1 after 0 => [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			arr.MoveAfterByIndex(0, 2)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "move 1 after 0"))
	})

	t.Run("can insert an element at the first of array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		var targetID *time.Ticket

		// Step 1: [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(0, 1, 2)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "initialize"))

		// Step 2: Move 2 to front => [2,0,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(2).CreatedAt()
			arr.MoveFront(targetID)
			assert.Equal(t, `{"list":[2,0,1]}`, root.Marshal())
			return nil
		}, "move 2 to front"))

		// Step 3: Move 0 to front => [0,2,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(1).CreatedAt() // 0 now at index 1
			arr.MoveFront(targetID)
			assert.Equal(t, `{"list":[0,2,1]}`, root.Marshal())
			return nil
		}, "move 0 to front"))

		// Step 4: Move 0 to front again (noop) => [0,2,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(0).CreatedAt() // 0 already at front
			arr.MoveFront(targetID)
			assert.Equal(t, `{"list":[0,2,1]}`, root.Marshal())
			return nil
		}, "noop move 0 again"))
	})

	t.Run("can move an element at the last of array (document-based)", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		var targetID *time.Ticket

		// Step 1: [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("list")
			arr.AddInteger(0, 1, 2)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "initialize list"))

		// Step 2: Move 2 to last (noop) => [0,1,2]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(2).CreatedAt()
			arr.MoveLast(targetID)
			assert.Equal(t, `{"list":[0,1,2]}`, root.Marshal())
			return nil
		}, "move 2 to last"))

		// Step 3: Move 1 to last => [0,2,1]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(1).CreatedAt() // 1
			arr.MoveLast(targetID)
			assert.Equal(t, `{"list":[0,2,1]}`, root.Marshal())
			return nil
		}, "move 1 to last"))

		// Step 4: Move 0 to last => [2,1,0]
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("list")
			targetID = arr.Get(0).CreatedAt() // 0
			arr.MoveLast(targetID)
			assert.Equal(t, `{"list":[2,1,0]}`, root.Marshal())
			return nil
		}, "move 0 to last"))
	})

}
