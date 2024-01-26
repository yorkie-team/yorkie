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
	gotime "time"

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

	t.Run("array.set with Counter, Text, Tree slice test", func(t *testing.T) {
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
			root.SetNewArray("counters", []*json.Counter{cnt, cnt, cnt})
			root.GetArray("counters").GetCounter(0).Increase(1)
			assert.Equal(t, `{"counters":[1,0,0]}`, root.Marshal())

			// Text
			root.SetNewArray("texts", []*json.Text{txt, txt, txt})
			root.GetArray("texts").GetText(0).Edit(0, 0, "hello")
			assert.Equal(t, `[[{"val":"hello"}],[],[]]`, root.GetArray("texts").Marshal())

			// Tree
			root.SetNewArray("forest", []*json.Tree{tree, tree})
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

func TestArraySetTypeGuard(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	type T1 struct {
		M string
	}

	typeGuardTests := []struct {
		caseName   string
		in         any
		isNotPanic bool
	}{
		{"nil", nil, false},
		{"struct", T1{"a"}, false},
		{"struct pointer", &T1{"a"}, false},
		{"int", 1, false},
		{"json.Counter", *json.NewCounter(1, crdt.LongCnt), false},
		{"json.Text", *json.NewText(), false},
		{"json.Tree", *json.NewTree(), false},
		{"&json.Counter", json.NewCounter(1, crdt.LongCnt), false},
		{"&json.Text", json.NewText(), false},
		{"&json.Tree", json.NewTree(), false},

		{"int slice", []int{1, 2, 3}, true},
		{"any slice", []any{1, 2, 3}, true},
		{"struct slice", []T1{{"a"}, {"b"}}, true},
		{"struct pointers slice", []*T1{{"a"}, {"b"}}, true},
		{"int array", [3]int{1, 2, 3}, true},
		{"any array", [3]any{1, 2, 3}, true},
		{"struct array", [2]T1{{"a"}, {"b"}}, true},
		{"struct pointers array", [2]*T1{{"a"}, {"b"}}, true},
	}

	for _, tt := range typeGuardTests {
		t.Run(tt.caseName, func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			val := func() {
				d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewArray("array", tt.in)
					return nil
				})
			}
			if tt.isNotPanic {
				assert.NotPanics(t, val)
			} else {
				assert.PanicsWithValue(t, "unsupported array type", val)
			}
		})
	}

}

func TestArraySet(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	type (
		T1 struct {
			M string
		}
		T2 struct {
			Skip  string `yorkie:"-"`
			M     string `yorkie:"m"`
			Empty string `yorkie:"e,omitEmpty"`
		}
	)

	arr := [3]int{1, 2, 3}
	t1 := T1{"a"}
	map1 := map[string]interface{}{"a": 1, "b": 2}

	tests := []struct {
		caseName   string
		in         any
		want       string
		tombstones int
	}{
		// primitive
		{"int", []int{1, 2, 3}, `[1,2,3]`, 4},
		{"int32", []int32{1, 2, 3}, `[1,2,3]`, 4},
		{"int64", []int64{1, 2, 3}, `[1,2,3]`, 4},
		{"float32", []float32{1.1, 2.2}, `[1.100000,2.200000]`, 3},
		{"float64", []float64{1.1, 2.2}, `[1.100000,2.200000]`, 3},
		{"string", []string{"a", "b", "c"}, `["a","b","c"]`, 4},
		{"bool", []bool{true, false, true}, `[true,false,true]`, 4},
		{"bytes", [][]byte{{65, 66}, {67, 68}}, `["AB","CD"]`, 3},
		{"time", []gotime.Time{gotime.Date(2022, 3, 2, 9, 10, 0, 0, gotime.UTC)}, `["2022-03-02T09:10:00Z"]`, 2},

		// json Counter, Text, Tree
		{"Counter", []json.Counter{*json.NewCounter(1, crdt.LongCnt)}, `[1]`, 2},
		{"Text", []json.Text{*json.NewText()}, `[[]]`, 2},
		{"Tree", []json.Tree{*json.NewTree()}, `[{"type":"root","children":[]}]`, 2},

		// user defined struct
		{"not initialized struct", []T1{{}, {}}, `[{"M":""},{"M":""}]`, 5},
		{"struct", []T1{{"a"}, {"b"}}, `[{"M":"a"},{"M":"b"}]`, 5},
		{"struct pointers", []*T1{&t1, &t1}, `[{"M":"a"},{"M":"a"}]`, 5},

		// user defined struct with tag
		{"not initialized struct", []T2{{}, {}}, `[{"m":""},{"m":""}]`, 5},
		{"tagged struct", []T2{{"", "2", ""}, {"", "2", ""}}, `[{"m":"2"},{"m":"2"}]`, 5},
		{"initialized struct", []T2{{"1", "2", "3"}, {"1", "2", "3"}}, `[{"e":"3","m":"2"},{"e":"3","m":"2"}]`, 7},

		// array
		{"array", [1][3]int{{1, 2, 3}}, `[[1,2,3]]`, 5},
		{"array pointers", [2]*[3]int{&arr, &arr}, `[[1,2,3],[1,2,3]]`, 9},

		// nested slice
		{"nested slice", [][]int{{1, 2, 3}, {1, 2, 3}}, `[[1,2,3],[1,2,3]]`, 9},

		// map
		{"map", []map[string]interface{}{map1, map1}, `[{"a":1,"b":2},{"a":1,"b":2}]`, 7},
		{"map pointers", []*map[string]interface{}{&map1, &map1}, `[{"a":1,"b":2},{"a":1,"b":2}]`, 7},
	}

	for _, tt := range tests {
		t.Run(tt.caseName, func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			err := d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewArray("array", tt.in)
				assert.Equal(t, tt.want, root.GetArray("array").Marshal())
				return nil
			})
			assert.NoError(t, err)

			err = d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.Delete("array")
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, tt.tombstones, d1.GarbageLen())
			assert.Equal(t, tt.tombstones, d1.GarbageCollect(time.MaxTicket))
		})
	}
}
