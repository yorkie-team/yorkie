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
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
			root.SetNewArray("k1", []interface{}{0, []interface{}{1, 2, 3}})
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
		assert.Equal(t, 8, d1.GarbageCollect(time.MaxTicket))
	})

	t.Run("array.set with value sync test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1", []interface{}{0, 1, 2})
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

	t.Run("array.set with Counter, Text, Tree array test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		cnt := json.NewCounter(0, crdt.LongCnt)
		txt := json.NewText()
		tree := json.NewTree()

		// 01. set array with value
		// 02. Edit value in array
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			// Counter
			root.SetNewArray("counters", []json.Counter{cnt, cnt, cnt})
			root.GetArray("counters").Get(0).(*json.Counter).Increase(1)
			assert.Equal(t, `{"counters":[1,0,0]}`, root.Marshal())

			// Text
			root.SetNewArray("texts", []json.Text{txt, txt, txt})
			root.GetArray("texts").Get(0).(*json.Text).Edit(0, 0, "hello")
			assert.Equal(t, `[[{"val":"hello"}],[],[]]`, root.GetArray("texts").Marshal())

			// Tree
			root.SetNewArray("forest", []json.Tree{tree, tree})
			root.GetArray("forest").Get(0).(*json.Tree).Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{},
			}, 0)
			assert.Equal(t, `[{"type":"root","children":[{"type":"p","children":[]}]},{"type":"root","children":[]}]`, root.GetArray("forest").Marshal())
			assert.Equal(t, `<root><p></p></root>`, root.GetArray("forest").Get(0).(*json.Tree).ToXML())
			return nil
		}))
	})

	t.Run("array.set with tagged struct test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		type T1 struct {
			private string
			Msg     string `yorkie:"msg,omitEmpty"`
			Num     int    `yorkie:"num"`
			Skip    string `yorkie:"-"`
		}

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("structs", []T1{{}, {"def", "def", 1, "skipped"}})
			assert.Equal(t, `[{"num":0},{"msg":"def","num":1}]`, root.GetArray("structs").Marshal())

			root.Delete("structs")
			return nil
		}))

		//Tombstones : array, struct1, int1, struct2, string2, int2
		assert.Equal(t, 6, d1.GarbageLen())
		assert.Equal(t, 6, d1.GarbageCollect(time.MaxTicket))
	})

	t.Run("array.set with typed array test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		arr := []int{1, 2, 3}

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("structs", []([]int){arr})
			assert.Equal(t, `[[1,2,3]]`, root.GetArray("structs").Marshal())

			root.Delete("structs")
			return nil
		}))

		//Tombstones : array, struct1, int1, struct2, string2, int2
		assert.Equal(t, 5, d1.GarbageLen())
		assert.Equal(t, 5, d1.GarbageCollect(time.MaxTicket))
	})
}
