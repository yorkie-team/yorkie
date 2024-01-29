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

func TestObject(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("causal object.set/delete test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1").
				SetString("k1.1", "v1").
				SetString("k1.2", "v2").
				SetString("k1.3", "v3")
			root.SetNewObject("k2").
				SetString("k2.1", "v4").
				SetString("k2.2", "v5").
				SetString("k2.3", "v6")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.GetObject("k2").Delete("k2.2")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object set/delete simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":{}}`, d1.Marshal())
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.SetString("k1", "v1")
			return nil
		}, "delete and set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v1"}`, d1.Marshal())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k1")
			root.SetString("k1", "v2")
			return nil
		}, "delete and set v2 by c2")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v2"}`, d2.Marshal())
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object.set test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// 01. concurrent set on same key
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}, "set k1 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v2")
			return nil
		}, "set k1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. concurrent set between ancestor descendant
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k2", "v2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("k2").SetNewObject("k2.1").SetString("k2.1.1", "v2")
			return nil
		}, "set k2.1.1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. concurrent set between independent keys
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k3", "v3")
			return nil
		}, "set k3 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k4", "v4")
			return nil
		}, "set k4 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("object.set with json literal test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 01. set new object with json literal. 10 elements will be created.
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj", map[string]interface{}{ // 1: object
				"str": "v",                                  // 1: string
				"arr": []interface{}{1, "str"},              // 3: array, 1, "str"
				"obj": map[string]interface{}{"key3": 42.2}, // 2: object, 42.2
				"cnt": json.NewCounter(0, crdt.LongCnt),     // 1: counter
				"txt": json.NewText(),                       // 1: text
				"tree": json.NewTree(&json.TreeNode{ // 1: tree
					Type: "doc",
					Children: []json.TreeNode{{
						Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
					}},
				}),
			})
			return nil
		})
		assert.NoError(t, err)

		// 02. set new object with same value by calling function.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj").SetString("str", "v")
			root.GetObject("obj").SetNewArray("arr").AddInteger(1).AddString("str")
			root.GetObject("obj").SetNewObject("obj").SetDouble("key3", 42.2)
			root.GetObject("obj").SetNewCounter("cnt", crdt.LongCnt, 0)
			root.GetObject("obj").SetNewText("txt")
			root.GetObject("obj").SetNewTree("tree", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"arr":[1,"str"],"cnt":0,"obj":{"key3":42.200000},"str":"v","tree":{"type":"doc","children":[{"type":"p","children":[{"type":"text","value":"ab"}]}]},"txt":[]}}`, d1.Marshal())
		assert.Equal(t, d2.Marshal(), d1.Marshal())

		// 03. remove the object and check the number of tombstones.
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("obj")
			return nil
		})
		assert.NoError(t, err)
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("obj")
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, 10, d1.GarbageLen())
		assert.Equal(t, 10, d1.GarbageCollect(time.MaxTicket))
		assert.Equal(t, 10, d2.GarbageLen())
		assert.Equal(t, 10, d2.GarbageCollect(time.MaxTicket))
	})

	t.Run("object.set with json literal sync test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 01. Sync set new object from d1 to d2
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape", map[string]interface{}{
				"color": "black",
			})
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. Sync overwrite object from d2 to d1
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("shape", map[string]interface{}{
				"color": "yellow",
			})
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. Sync delete object from d2 to d1
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("shape").Delete("color")
			return nil
		})
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("object.set with json literal array type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set nested array in object with json literal
			root.SetNewObject("obj", map[string]interface{}{
				"array": []interface{}{1, 2, 3, []int{7, 8}},
			})
			assert.Equal(t, `{"obj":{"array":[1,2,3,[7,8]]}}`, root.Marshal())

			// 02. delete nested array in object
			root.GetObject("obj").GetArray("array").Delete(3)
			assert.Equal(t, `{"obj":{"array":[1,2,3]}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. garbage collect (3 elements: array, 1, 2)
		assert.Equal(t, 3, d1.GarbageLen())
		assert.Equal(t, 3, d1.GarbageCollect(time.MaxTicket))
	})

	t.Run("object.set with json literal counter type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set two type of counter in object with json literal
			root.SetNewObject("obj", map[string]interface{}{
				"cntLong": json.NewCounter(0, crdt.LongCnt),
				"cntInt":  json.NewCounter(0, crdt.IntegerCnt),
			})
			assert.Equal(t, `{"obj":{"cntInt":0,"cntLong":0}}`, root.Marshal())

			// 02. increase and decrease counter
			root.GetObject("obj").GetCounter("cntLong").Increase(1)
			root.GetObject("obj").GetCounter("cntInt").Increase(-1)
			assert.Equal(t, `{"obj":{"cntInt":-1,"cntLong":1}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. is it also applied to the document
		assert.Equal(t, `{"obj":{"cntInt":-1,"cntLong":1}}`, d1.Marshal())
	})

	t.Run("object.set with json literal text type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. set text in object with json literal
			root.SetNewObject("obj", map[string]interface{}{
				"txt": json.NewText(),
			})
			assert.Equal(t, `{"obj":{"txt":[]}}`, root.Marshal())

			// 02. edit text
			root.GetObject("obj").GetText("txt").Edit(0, 0, "ABCD")
			assert.Equal(t, `{"obj":{"txt":[{"val":"ABCD"}]}}`, root.Marshal())
			return nil
		})
		assert.NoError(t, err)

		// 03. is it also applied to the document
		assert.Equal(t, `{"obj":{"txt":[{"val":"ABCD"}]}}`, d1.Marshal())
	})

	t.Run("object.set with json literal primitive type test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj", map[string]interface{}{
				"nill":   nil,
				"bool":   true,
				"long":   9223372036854775807,
				"int":    32,
				"double": 1.79,
				"bytes":  []byte{65, 66},
				"date":   gotime.Date(2022, 3, 2, 9, 10, 0, 0, gotime.UTC),
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"bool":true,"bytes":"AB","date":"2022-03-02T09:10:00Z","double":1.790000,"int":32,"long":9223372036854775807,"nill":null}}`, d1.Marshal())
	})
}

func TestObjectTypeGuard(t *testing.T) {
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
		{"slice", []int{1, 2, 3}, false},
		{"&slice", &[]int{1, 2, 3}, false},
		{"array", [3]int{1, 2, 3}, false},
		{"&array", &[3]int{1, 2, 3}, false},
		{"map int", map[string]int{}, false},
		{"&map int", &map[string]int{}, false},
		{"int map", map[int]any{}, false},
		{"&int map", &map[int]any{}, false},
		{"&json.Text", json.NewText(), false},
		{"json.Text", *json.NewText(), false},
		{"&json.Tree", json.NewTree(), false},
		{"json.Tree", *json.NewTree(), false},
		{"&json.Counter", json.NewCounter(0, crdt.LongCnt), false},
		{"json.Counter", *json.NewCounter(0, crdt.LongCnt), false},

		{"map any", map[string]any{}, true},
		{"&map", &map[string]any{}, true},
		{"struct", struct{}{}, true},
		{"&struct", &struct{}{}, true},
		{"defined struct", T1{}, true},
		{"&defined struct", &T1{}, true},
	}

	for _, tt := range typeGuardTests {
		t.Run(tt.caseName, func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			val := func() {
				d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewObject("obj", tt.in)
					return nil
				})
			}

			if tt.isNotPanic {
				assert.NotPanics(t, val)
			} else {
				assert.PanicsWithValue(t, "unsupported object type", val)
			}
		})
	}
}

func TestObjectSetCycle(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	type (
		PointerCycle struct {
			Next *PointerCycle
		}
		PointerCycleIndirect struct {
			Ptrs []any
		}
		RecursiveSlice []RecursiveSlice
		T1             struct {
			M RecursiveSlice
		}
	)

	// Unsupported types
	var (
		pointerCycle         = &PointerCycle{}
		pointerCycleIndirect = &PointerCycleIndirect{}
		mapCycle             = map[string]any{}
		sliceCycle           = []any{nil}
		recursiveSliceCycle  = []RecursiveSlice{nil}
	)

	// Initialize
	pointerCycle.Next = pointerCycle
	pointerCycleIndirect.Ptrs = []any{pointerCycleIndirect}
	mapCycle["k1"] = mapCycle
	sliceCycle[0] = sliceCycle
	recursiveSliceCycle[0] = recursiveSliceCycle

	cycleTests := []struct {
		caseName string
		in       any
	}{
		{"pointer cycle", pointerCycle},
		{"pointer cycle indirect", pointerCycleIndirect},
		{"map cycle", mapCycle},
		{"slice cycle", map[string]any{"k1": sliceCycle}},
		{"recursive slice cycle", T1{M: recursiveSliceCycle}},
	}

	for _, tt := range cycleTests {
		t.Run(tt.caseName, func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			val := func() {
				d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewObject("obj", tt.in)
					return nil
				})
			}
			assert.PanicsWithValue(t, "cycle detected", val)
		})
	}
}

func TestObjectSet(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	type (
		Myint    int
		MyStruct struct {
			M Myint
		}

		t1 struct {
			M string
		}

		T1 struct {
			M string
		}
		T2 struct {
			M *string
		}
		T3 struct {
			C json.Counter
		}
		T4 struct {
			T1
			t1
			M string
		}
	)

	empty := ""
	str := "foo"
	emptyTarget := `{"obj":{"M":""}}`
	strTarget := `{"obj":{"M":"foo"}}`

	embedded := T4{
		T1: T1{M: str},
		t1: t1{M: str},
		M:  "foo",
	}

	type MyInt int
	type S struct{ MyInt }

	setTests := []struct {
		caseName   string
		in         any
		want       string
		tombstones int
	}{
		// Test nll
		{"null map", map[string]any{"M": nil}, `{"obj":{"M":null}}`, 2},
		{"null &map", &map[string]any{"M": nil}, `{"obj":{"M":null}}`, 2},

		// Test zero value
		{"zeroValue int struct", struct{ M int }{}, `{"obj":{"M":0}}`, 2},
		{"zeroValue string struct", struct{ M string }{}, `{"obj":{"M":""}}`, 2},
		{"zeroValue bytes struct", struct{ M []byte }{M: nil}, `{"obj":{"M":""}}`, 2},
		{"empty bytes struct", struct{ M []byte }{M: []byte{}}, `{"obj":{"M":""}}`, 2},
		{"zeroValue array struct", struct{ M []int }{M: nil}, `{"obj":{"M":[]}}`, 2},
		{"empty array struct", struct{ M []int }{M: []int{}}, `{"obj":{"M":[]}}`, 2},

		// Test with empty string
		{"empty map", map[string]any{"M": empty}, emptyTarget, 2},
		{"empty &map", &map[string]any{"M": empty}, emptyTarget, 2},
		{"&empty map", map[string]any{"M": &empty}, emptyTarget, 2},
		{"&empty &map", &map[string]any{"M": &empty}, emptyTarget, 2},
		{"empty struct", struct{ M string }{M: empty}, emptyTarget, 2},
		{"empty &struct", &struct{ M string }{M: empty}, emptyTarget, 2},
		{"&empty struct", struct{ M *string }{M: &empty}, emptyTarget, 2},
		{"&empty &struct", &struct{ M *string }{M: &empty}, emptyTarget, 2},
		{"empty T1", T1{M: empty}, emptyTarget, 2},
		{"empty &T1", &T1{M: empty}, emptyTarget, 2},
		{"empty T2", T2{M: &empty}, emptyTarget, 2},
		{"empty &T2", &T2{M: &empty}, emptyTarget, 2},

		// Test with some str
		{"str map", map[string]any{"M": str}, strTarget, 2},
		{"str &map", &map[string]any{"M": str}, strTarget, 2},
		{"&str map", map[string]any{"M": &str}, strTarget, 2},
		{"&str &map", &map[string]any{"M": &str}, strTarget, 2},
		{"str struct", struct{ M string }{M: str}, strTarget, 2},
		{"str &struct", &struct{ M string }{M: str}, strTarget, 2},
		{"&str struct", struct{ M *string }{M: &str}, strTarget, 2},
		{"&str &struct", &struct{ M *string }{M: &str}, strTarget, 2},
		{"str T", T1{M: str}, strTarget, 2},
		{"str &T", &T1{M: str}, strTarget, 2},
		{"str T2", T2{M: &str}, strTarget, 2},
		{"str &T2", &T2{M: &str}, strTarget, 2},

		// Test with unexported field
		{"unexported field in struct", struct{ m string }{m: str}, `{"obj":{}}`, 1},

		// Test with - Tag
		{"- tagged in struct", struct {
			M1 string `yorkie:"-"`
			M2 string
		}{M1: str, M2: str}, `{"obj":{"M2":"foo"}}`, 2},

		// Test with omitempty Tag
		{"omitEmpty tagged in struct", struct {
			M1 string `yorkie:",omitEmpty"`
			M2 string `yorkie:",omitEmpty"`
		}{M1: str}, `{"obj":{"M1":"foo"}}`, 2},
		{"omitEmpty tagged array in struct", struct {
			M1 []int `yorkie:",omitEmpty"`
		}{}, `{"obj":{}}`, 1},
		{"omitEmpty tagged empty array in struct", struct {
			M1 []int `yorkie:",omitEmpty"`
		}{M1: []int{}}, `{"obj":{}}`, 1},

		// Test with field name Tag
		{"field name tagged in struct", struct {
			M1 string `yorkie:"m1"`
			M2 string `yorkie:"m2"`
		}{M1: str, M2: str}, `{"obj":{"m1":"foo","m2":"foo"}}`, 3},

		// Test Tree, Text, Counter, Object
		// uninitialized
		{"counter in struct", struct{ M json.Counter }{}, `{"obj":{"M":0}}`, 2},
		{"Text in struct", struct{ M json.Text }{}, `{"obj":{"M":[]}}`, 2},
		{"Tree in struct", struct{ M json.Tree }{}, `{"obj":{"M":{"type":"root","children":[]}}}`, 2},
		{"Object in struct", struct{ M json.Object }{}, `{"obj":{"M":{}}}`, 2},
		// empty initialized
		{"Text in struct", struct{ M json.Text }{M: *json.NewText()}, `{"obj":{"M":[]}}`, 2},
		{"Tree in struct", struct{ M json.Tree }{M: *json.NewTree()}, `{"obj":{"M":{"type":"root","children":[]}}}`, 2},
		// initialized
		{"counter in struct", struct{ M json.Counter }{M: *json.NewCounter(0, crdt.LongCnt)}, `{"obj":{"M":0}}`, 2},
		{"Tree in struct", struct{ M json.Tree }{M: *json.NewTree(&json.TreeNode{
			Type:     "p",
			Children: []json.TreeNode{},
		})}, `{"obj":{"M":{"type":"p","children":[]}}}`, 2},
		// Tagged
		{"counter in struct", struct {
			M1 json.Counter `yorkie:"-"`
			M2 json.Text    `yorkie:"-"`
			M3 json.Tree    `yorkie:"-"`
			M4 json.Object  `yorkie:"-"`
		}{}, `{"obj":{}}`, 1},
		{"counter in struct", struct {
			M1 json.Counter `yorkie:",omitEmpty"`
			M2 json.Text    `yorkie:",omitEmpty"`
			M3 json.Tree    `yorkie:",omitEmpty"`
			M4 json.Object  `yorkie:",omitEmpty"`
		}{}, `{"obj":{}}`, 1},

		// Test with nested struct
		{"nested user defined struct", struct{ M T1 }{M: T1{M: str}}, `{"obj":{"M":{"M":"foo"}}}`, 3},
		{"nested &user defined struct", struct{ M *T1 }{M: &T1{M: str}}, `{"obj":{"M":{"M":"foo"}}}`, 3},
		{"nested user defined struct with zero value", struct{ M T1 }{}, `{"obj":{"M":{"M":""}}}`, 3},
		{"nested &user defined struct with nil", struct{ M *T1 }{M: nil}, `{"obj":{"M":null}}`, 2},
		{"nested object with json.Counter", struct{ M T3 }{M: T3{C: *json.NewCounter(0, crdt.LongCnt)}}, `{"obj":{"M":{"C":0}}}`, 3},
		{"nested &object with json.Counter", struct{ M *T3 }{M: &T3{C: *json.NewCounter(0, crdt.LongCnt)}}, `{"obj":{"M":{"C":0}}}`, 3},
		{"nested object with json.Counter with zero value", struct{ M T3 }{}, `{"obj":{"M":{"C":0}}}`, 3},

		//Test with embedded struct
		{"embedded struct", embedded, `{"obj":{"M":"foo","T1":{"M":"foo"}}}`, 4},
		{"&embedded struct", &embedded, `{"obj":{"M":"foo","T1":{"M":"foo"}}}`, 4},

		// Test with anonymous field in struct
		{"anonymous field in struct", S{5}, `{"obj":{"MyInt":5}}`, 2},

		// Test with named type field in struct
		{"named type field in struct", struct{ M MyInt }{M: 5}, `{"obj":{"M":5}}`, 2},
		{"named type field in struct", MyStruct{5}, `{"obj":{"M":5}}`, 2},
	}

	for _, tt := range setTests {
		t.Run(tt.caseName, func(t *testing.T) {
			ctx := context.Background()
			d1 := document.New(helper.TestDocKey(t))
			assert.NoError(t, c1.Attach(ctx, d1))

			err := d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("obj", tt.in)
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, tt.want, d1.Marshal())

			err = d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.Delete("obj")
				return nil
			})
			assert.NoError(t, err)
			assert.Equal(t, tt.tombstones, d1.GarbageLen())
			assert.Equal(t, tt.tombstones, d1.GarbageCollect(time.MaxTicket))
		})
	}

	t.Run("object.set with JSON.Object", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		type T struct {
			M json.Object
		}

		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("obj", map[string]interface{}{
				"key": "value",
			})

			root.SetNewObject("obj2", T{M: *root.GetObject("obj")})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"obj":{"key":"value"},"obj2":{"M":{}}}`, d1.Marshal())
	})
}
