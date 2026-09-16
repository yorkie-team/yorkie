/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package yson_test

import (
	"encoding/base64"
	"fmt"
	"math"
	"strings"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
)

func TestYSONConversion(t *testing.T) {
	t.Run("json struct conversion", func(t *testing.T) {
		doc := document.New("yson")

		err := doc.Update(func(r *json.Object, p *presence.Presence) error {
			// an object and primitive types
			r.SetNewObject("k1").
				SetNull("k1.0").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", gotime.Now()).
				Delete("k1.5")

			// an array
			r.SetNewArray("k2").
				AddNull().
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(gotime.Now()).
				Delete(4)

			// plain text
			r.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘").
				Edit(2, 2, "구름").
				Edit(2, 3, "뭉게구")

			// rich text
			r.SetNewText("k4").
				Edit(0, 0, "Hello world", nil).
				Edit(6, 11, "sky", map[string]string{"color": "red"}).
				Style(0, 5, map[string]string{"b": "1"}).
				Style(6, 9, map[string]string{"color": "blue"})

			// long counter
			r.SetNewCounter("k5", int64(0)).
				Increase(10)

			// integer counter
			r.SetNewCounter("k6", 0).
				Increase(10)

			// tree
			r.SetNewTree("k7").
				Edit(0, 0, &yson.TreeNode{
					Type:     "p",
					Children: []yson.TreeNode{{Type: "text", Value: "Hello world"}},
				}, 0)
			return nil
		})
		assert.NoError(t, err)

		root, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)

		newDoc := document.New("yson")
		err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(root)
			return nil
		})
		assert.NoError(t, err)

		// verify the conversion
		assert.Equal(t, doc.Marshal(), newDoc.Marshal())
		newRoot, err := yson.FromCRDT(newDoc.RootObject())
		assert.NoError(t, err)

		prevMarshalled, err := root.(yson.Object).Marshal()
		assert.NoError(t, err)
		newMarshalled, err := newRoot.(yson.Object).Marshal()
		assert.NoError(t, err)
		assert.Equal(t, prevMarshalled, newMarshalled)
	})

	t.Run("array with nested types test", func(t *testing.T) {
		doc := document.New("nested-types")

		err := doc.Update(func(r *json.Object, p *presence.Presence) error {
			arr := r.SetNewArray("nested")

			// Add nested array
			nestedArr := arr.AddNewArray()
			nestedArr.AddString("nested1")
			nestedArr.AddInteger(42)

			// Add nested object
			obj := arr.AddNewObject()
			obj.SetString("key", "value")
			obj.SetNewCounter("counter", 10)

			text := arr.AddNewText()
			text.Edit(0, 0, "Hello")
			text.Edit(5, 5, " World")
			text.Style(0, 5, map[string]string{"bold": "true"})

			// Add nested tree
			arr.AddNewTree(yson.TreeNode{
				Type: "p",
				Children: []yson.TreeNode{
					{Type: "text", Value: "Tree in array"},
					{
						Type:       "span",
						Attributes: map[string]string{"style": "color: red"},
						Children:   []yson.TreeNode{{Type: "text", Value: "Styled text"}},
					},
				},
			})

			return nil
		})
		assert.NoError(t, err)

		// Convert to YSON
		root, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)

		// Convert back to document
		newDoc := document.New("nested-types")
		err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(root)
			return nil
		})
		assert.NoError(t, err)

		// Verify the conversion
		assert.Equal(t, doc.Marshal(), newDoc.Marshal())
		newRoot, err := yson.FromCRDT(newDoc.RootObject())
		assert.NoError(t, err)

		prevMarshalled, err := root.(yson.Object).Marshal()
		assert.NoError(t, err)
		newMarshalled, err := newRoot.(yson.Object).Marshal()
		assert.NoError(t, err)
		assert.Equal(t, prevMarshalled, newMarshalled)
	})

	t.Run("dedup counter full round-trip test", func(t *testing.T) {
		doc := document.New("dedup-roundtrip")
		err := doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetNewCounter("pv", 0).Increase(100)
			counter := r.SetNewDedupCounter("uv")
			for i := 0; i < 10; i++ {
				counter.Add(fmt.Sprintf("user-%d", i))
			}
			return nil
		})
		assert.NoError(t, err)

		// Convert CRDT → YSON
		root, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)

		// Convert YSON → new doc (simulates compaction rebuild)
		newDoc := document.New("dedup-roundtrip")
		err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(root)
			return nil
		})
		assert.NoError(t, err)

		// Convert new doc back to YSON
		newRoot, err := yson.FromCRDT(newDoc.RootObject())
		assert.NoError(t, err)

		// Marshal both and compare (compaction invariant)
		prevMarshalled, err := root.(yson.Object).Marshal()
		assert.NoError(t, err)
		newMarshalled, err := newRoot.(yson.Object).Marshal()
		assert.NoError(t, err)
		assert.Equal(t, prevMarshalled, newMarshalled)

		// Also verify the client-visible marshal matches
		assert.Equal(t, doc.Marshal(), newDoc.Marshal())
	})

	t.Run("dedup counter CRDT conversion test", func(t *testing.T) {
		doc := document.New("dedup-yson")

		err := doc.Update(func(r *json.Object, p *presence.Presence) error {
			counter := r.SetNewDedupCounter("uv")
			for i := 0; i < 5; i++ {
				counter.Add(fmt.Sprintf("user-%d", i))
			}
			return nil
		})
		assert.NoError(t, err)

		root, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)

		// Verify HLL registers are present in the YSON counter
		ysonObj := root.(yson.Object)
		ysonCounter := ysonObj["uv"].(yson.Counter)
		assert.Equal(t, crdt.IntegerDedupCnt, ysonCounter.Type)
		assert.Equal(t, int32(5), ysonCounter.Value)
		assert.NotNil(t, ysonCounter.Registers)
		assert.Equal(t, 16384, len(ysonCounter.Registers))
	})

	t.Run("yson conversion test", func(t *testing.T) {
		root := yson.Object{
			"string": "string",
			"int":    int32(32),
			"long":   int64(64),
			"null":   nil,
			"bool":   true,
			"bytes":  []byte{1, 2, 3},
			"date":   gotime.Now(),
			"nested": yson.Array{
				yson.Array{"string", int32(32), int64(64), nil, true,
					[]byte{1, 2, 3}, gotime.Now(), yson.Object{"nested": "nest-obj"}},
				yson.Object{
					"counter": yson.Counter{Type: crdt.IntegerCnt, Value: int32(10)},
					"key":     "value",
				},
				yson.Text{
					Nodes: []yson.TextNode{
						{
							Value:      "Hello",
							Attributes: map[string]string{"style": "color: red"},
						},
						{
							Value:      "World",
							Attributes: map[string]string{"style": "color: blue"},
						},
					},
				},
				yson.Tree{
					Root: yson.TreeNode{
						Type: "p",
						Children: []yson.TreeNode{
							{Type: "text", Value: "Tree in array"},
							{
								Type:       "span",
								Attributes: map[string]string{"style": "color: red"},
								Children:   []yson.TreeNode{{Type: "text", Value: "Styled text"}},
							},
						},
					},
				},
			},
		}
		doc := document.New("yson")
		err := doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetYSON(root)
			return nil
		})
		assert.NoError(t, err)
		newRoot, err := yson.FromCRDT(doc.RootObject())
		assert.NoError(t, err)
		assert.Equal(t, root, newRoot)
	})
}

func TestYSONMarshal(t *testing.T) {
	t.Run("object marshal/unmarshal test", func(t *testing.T) {
		obj := yson.Object{
			"key1": "value1",
			"key2": int32(42),
			"key3": int64(64),
			"key4": nil,
			"key5": true,
			"key6": []byte{1, 2, 3},
			"key7": gotime.Date(2025, 1, 2, 15, 4, 5, 58000000, gotime.UTC),
			"key8": yson.Counter{Type: crdt.IntegerCnt, Value: int32(10)},
			"key9": yson.Tree{
				Root: yson.TreeNode{
					Type:     "p",
					Children: []yson.TreeNode{{Type: "text", Value: "Tree in object"}},
				},
			},
		}
		actual := yson.Object{}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))

		expected := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{
			"key1": "value1",
			"key2": Int(42),
			"key3": Long(64),
			"key4": null,
			"key5": true,
			"key6": BinData("AQID"),
			"key7": Date("2025-01-02T15:04:05.058Z"),
			"key8": Counter(Int(10)),
			"key9": Tree({"type":"p","children":[{"type":"text","value":"Tree in object"}]})
		}`, &expected))
		assert.Equal(t, expected, actual)
	})

	t.Run("array marshal/unmarshal test", func(t *testing.T) {
		arr := yson.Array{
			"hello",
			int32(32),
			int64(64),
			1.23,
			nil,
			true,
			[]byte{1, 2, 3},
			gotime.Date(2025, 1, 2, 15, 4, 5, 58000000, gotime.UTC),
			yson.Counter{Type: crdt.IntegerCnt, Value: int32(32)},
			yson.Counter{Type: crdt.LongCnt, Value: int64(64)},
			yson.Array{"nested", int64(1)},
			yson.Object{"nested": "nest-obj"},
			yson.Tree{
				Root: yson.TreeNode{
					Type:     "p",
					Children: []yson.TreeNode{{Type: "text", Value: "Tree in array"}},
				},
			},
			yson.Text{
				Nodes: []yson.TextNode{
					{Value: "Hello", Attributes: map[string]string{"color": "red"}},
					{Value: "World", Attributes: map[string]string{"color": "blue"}},
				},
			},
		}
		actual := yson.Array{}
		marshalled, err := arr.Marshal()
		assert.NoError(t, err)
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))

		expected := yson.Array{}
		assert.NoError(t, yson.Unmarshal(`[
            "hello",
            Int(32),
            Long(64),
            1.23,
            null,
            true,
            BinData("AQID"),
            Date("2025-01-02T15:04:05.058Z"),
            Counter(Int(32)),
            Counter(Long(64)),
            ["nested",Long(1)],
            {"nested":"nest-obj"},
            Tree({"type":"p","children":[{"type":"text","value":"Tree in array"}]}),
            Text([{"val":"Hello","attrs":{"color":"red"}},{"val":"World","attrs":{"color":"blue"}}])
        ]`, &expected))
		assert.Equal(t, expected, actual)
	})

	t.Run("counter marshal/unmarshal test", func(t *testing.T) {
		counter := yson.Counter{Type: crdt.LongCnt, Value: int64(100)}
		actual := yson.Counter{}
		marshalled, err := counter.Marshal()
		assert.NoError(t, err)
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))

		expected := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(`Counter(Long(100))`, &expected))
		assert.Equal(t, expected, actual)
	})

	t.Run("large long value precision test", func(t *testing.T) {
		// 9007199254740993 (2^53 + 1) and math.MaxInt64 are not exactly
		// representable as float64, so they must be parsed from the lexeme.
		arr := yson.Array{}
		assert.NoError(t, yson.Unmarshal(`[Long(9007199254740993),Long(9223372036854775807)]`, &arr))
		assert.Equal(t, yson.Array{int64(9007199254740993), int64(math.MaxInt64)}, arr)

		counter := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(`Counter(Long(9223372036854775807))`, &counter))
		assert.Equal(t, int64(math.MaxInt64), counter.Value)
	})

	t.Run("dedup counter marshal test", func(t *testing.T) {
		// Create a 16384-byte register array with known values
		registers := make([]byte, 16384)
		registers[0] = 5
		registers[100] = 3

		counter := yson.Counter{
			Type:      crdt.IntegerDedupCnt,
			Value:     int32(15),
			Registers: registers,
		}
		marshalled, err := counter.Marshal()
		assert.NoError(t, err)

		expected := fmt.Sprintf(`DedupCounter(Int(15),"%s")`,
			base64.StdEncoding.EncodeToString(registers))
		assert.Equal(t, expected, marshalled)
	})

	t.Run("dedup counter unmarshal test", func(t *testing.T) {
		registers := make([]byte, 16384)
		registers[0] = 5
		registers[100] = 3
		encoded := base64.StdEncoding.EncodeToString(registers)

		input := fmt.Sprintf(`DedupCounter(Int(15),"%s")`, encoded)
		actual := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(input, &actual))

		assert.Equal(t, crdt.IntegerDedupCnt, actual.Type)
		assert.Equal(t, int32(15), actual.Value)
		assert.Equal(t, registers, actual.Registers)
	})

	t.Run("dedup counter round-trip test", func(t *testing.T) {
		registers := make([]byte, 16384)
		registers[0] = 5
		registers[100] = 3

		original := yson.Counter{
			Type:      crdt.IntegerDedupCnt,
			Value:     int32(15),
			Registers: registers,
		}
		marshalled, err := original.Marshal()
		assert.NoError(t, err)

		restored := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(marshalled, &restored))
		assert.Equal(t, original, restored)
	})

	t.Run("dedup counter in object round-trip test", func(t *testing.T) {
		registers := make([]byte, 16384)
		registers[42] = 7

		obj := yson.Object{
			"pv": yson.Counter{Type: crdt.IntegerCnt, Value: int32(100)},
			"uv": yson.Counter{
				Type:      crdt.IntegerDedupCnt,
				Value:     int32(15),
				Registers: registers,
			},
		}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		restored := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &restored))
		assert.Equal(t, obj, restored)
	})

	t.Run("text marshal/unmarshal test", func(t *testing.T) {
		text := yson.Text{
			Nodes: []yson.TextNode{
				{Value: "Hello", Attributes: map[string]string{"font": "bold"}},
				{Value: "World", Attributes: map[string]string{"color": "blue"}},
			},
		}
		actual := yson.Text{}
		marshalled, err := text.Marshal()
		assert.NoError(t, err)
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))

		expected := yson.Text{}
		assert.NoError(t, yson.Unmarshal(`Text([
			{"val":"Hello","attrs":{"font":"bold"}},
			{"val":"World","attrs":{"color":"blue"}}
		])`, &expected))
		assert.Equal(t, expected, actual)
	})

	t.Run("text only marshal/unmarshal test", func(t *testing.T) {
		initialRoot := yson.Object{
			"text": yson.Text{Nodes: []yson.TextNode{{Value: "Hello"}, {Value: "World"}}},
		}
		marshalled, err := initialRoot.Marshal()
		assert.NoError(t, err)

		actualRoot := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actualRoot))
		assert.Equal(t, initialRoot, actualRoot)
	})

	t.Run("tree marshal/unmarshal test", func(t *testing.T) {
		tree := yson.Tree{
			Root: yson.TreeNode{
				Type: "div",
				Children: []yson.TreeNode{
					{Type: "text", Value: "Hello Tree"},
					{
						Type:       "span",
						Attributes: map[string]string{"style": "color:green"},
						Children:   []yson.TreeNode{{Type: "text", Value: "Styled Tree"}},
					},
				},
			},
		}
		actual := yson.Tree{}
		marshalled, err := tree.Marshal()
		assert.NoError(t, err)
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))

		expected := yson.Tree{}
		assert.NoError(t, yson.Unmarshal(`Tree({"type":"div","children":[
			{"type":"text","value":"Hello Tree"},
			{"type":"span","attrs":{"style":"color:green"},"children":[{"type":"text","value":"Styled Tree"}]}
		]})`, &expected))
		assert.Equal(t, expected, actual)
	})

	t.Run("error handling test", func(t *testing.T) {
		testCases := []struct {
			name        string
			input       string
			targetType  yson.Element
			expectedErr string
		}{
			{
				name:        "invalid JSON",
				input:       `{invalid json`,
				targetType:  &yson.Object{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "unknown type",
				input:       `Unknown(123)`,
				targetType:  &yson.Object{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "invalid counter type",
				input:       `Counter(String("invalid"))`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "invalid tree format",
				input:       `Tree("not an object")`,
				targetType:  &yson.Tree{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "invalid text format",
				input:       `Text("not an array")`,
				targetType:  &yson.Text{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "missing required field in text node",
				input:       `Text([{"attrs":{"bold":"true"}}])`,
				targetType:  &yson.Text{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "fractional Int typed value",
				input:       `[Int(1.5)]`,
				targetType:  &yson.Array{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "out-of-range Int typed value",
				input:       `[Int(2147483648)]`,
				targetType:  &yson.Array{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "fractional Long typed value",
				input:       `[Long(1.5)]`,
				targetType:  &yson.Array{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "out-of-range Long typed value",
				input:       `[Long(9223372036854775808)]`,
				targetType:  &yson.Array{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "fractional counter Int value",
				input:       `Counter(Int(1.5))`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "out-of-range counter Int value",
				input:       `Counter(Int(2147483648))`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "fractional dedup counter value",
				input:       `DedupCounter(Int(1.5),"AQ==")`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "out-of-range dedup counter value",
				input:       `DedupCounter(Int(2147483648),"AQ==")`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "text node attrs wrong type",
				input:       `Text([{"val":"hi","attrs":"nope"}])`,
				targetType:  &yson.Text{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "text node attrs number",
				input:       `Text([{"val":"hi","attrs":1}])`,
				targetType:  &yson.Text{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "tree node attrs wrong type",
				input:       `Tree({"type":"p","attrs":"nope","children":[]})`,
				targetType:  &yson.Tree{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "tree node children wrong type",
				input:       `Tree({"type":"p","children":{"not":"an array"}})`,
				targetType:  &yson.Tree{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "tree node children string",
				input:       `Tree({"type":"p","children":"nope"})`,
				targetType:  &yson.Tree{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "trailing closing bracket",
				input:       `{"a":1}]`,
				targetType:  &yson.Object{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "trailing closing brace",
				input:       `{"a":1}}`,
				targetType:  &yson.Object{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "trailing second value",
				input:       `{"a":1} 42`,
				targetType:  &yson.Object{},
				expectedErr: "invalid YSON",
			},
			{
				name:        "trailing second object",
				input:       `[1,2]{"b":2}`,
				targetType:  &yson.Array{},
				expectedErr: "invalid YSON",
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				err := yson.Unmarshal(tc.input, tc.targetType)
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedErr)
			})
		}
	})
}

func TestYSONParse(t *testing.T) {
	t.Run("array parse test", func(t *testing.T) {
		input := `[
			Text(),
			Tree()
		]`

		assert.Equal(t, yson.ParseArray(input), yson.Array{
			yson.Text{},
			yson.Tree{Root: yson.TreeNode{Type: yson.DefaultRootNodeType}},
		})
	})
}

// textOf builds a single-node Text with the given value for round-trip tests.
func textOf(value string) yson.Text {
	return yson.Text{Nodes: []yson.TextNode{{Value: value}}}
}

func TestYSONStringAwareParsing(t *testing.T) {
	t.Run("text value with closing paren round-trip", func(t *testing.T) {
		obj := yson.Object{"c": textOf("see figure (1)")}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		actual := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Equal(t, obj, actual)
	})

	t.Run("text values with structural characters round-trip", func(t *testing.T) {
		values := []string{
			"close paren )",
			"close brace }",
			"close bracket ]",
			"open bracket [",
			"open brace {",
			"open paren (",
			"mixed )}]{[( soup",
		}
		for _, v := range values {
			obj := yson.Object{"c": textOf(v)}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual))
			assert.Equal(t, obj, actual, "value %q", v)
		}
	})

	t.Run("text values with constructor-like substrings round-trip", func(t *testing.T) {
		values := []string{
			"use Int( here",
			"use Text( here",
			"use Tree( here",
			"use Counter( here",
			"use Long( here",
			"use Date( here",
			"use BinData( here",
			"use DedupCounter(Int(5),\"x\") here",
			"Int(42) is a number",
			"Tree({}) is empty",
		}
		for _, v := range values {
			obj := yson.Object{"c": textOf(v)}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual))
			assert.Equal(t, obj, actual, "value %q", v)
		}
	})

	t.Run("object keys with structural characters round-trip", func(t *testing.T) {
		keys := []string{
			`quote " inside`,
			`backslash \ inside`,
			`trailing backslash \`,
			"colon : and comma ,",
			"close brace } and bracket ]",
			"use Int( here",
			"Int(42) is a number",
			"newline \n inside",
		}
		for _, k := range keys {
			obj := yson.Object{k: int32(1)}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual), "key %q", k)
			assert.Equal(t, obj, actual, "key %q", k)
		}
	})

	t.Run("object key cannot forge a sibling member", func(t *testing.T) {
		obj := yson.Object{`a":Int(1),"b`: int32(1)}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		actual := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Len(t, actual, 1)
		assert.Equal(t, obj, actual)
	})

	t.Run("control characters round-trip in keys and values", func(t *testing.T) {
		// A Go literal quoter writes these as \v or \x00, which JSON cannot
		// read back, so a document holding one would marshal to a snapshot that
		// never reloads.
		for _, s := range []string{
			"vertical tab \v here",
			"nul \x00 here",
			"bell \a here",
			"unit separator \x1f here",
			"delete \x7f here",
			"tab \t and newline \n here",
		} {
			obj := yson.Object{
				s:     int32(1),
				"str": s,
				"txt": yson.Text{Nodes: []yson.TextNode{{
					Value:      s,
					Attributes: map[string]string{s: s},
				}}},
				"tree": yson.Tree{Root: yson.TreeNode{
					Type:       "p",
					Attributes: map[string]string{s: s},
					Children:   []yson.TreeNode{{Type: "text", Value: s}},
				}},
			}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual), "string %q", s)
			assert.Equal(t, obj, actual, "string %q", s)
		}
	})

	t.Run("escaped quote adjacent to bracket in string round-trip", func(t *testing.T) {
		values := []string{
			`quote before bracket "] here`,
			`bracket then quote [" here`,
			`paren then escaped quote (\ okay`,
			`he said ")" loudly`,
		}
		for _, v := range values {
			obj := yson.Object{"c": textOf(v)}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual))
			assert.Equal(t, obj, actual, "value %q", v)
		}
	})

	t.Run("text and tree in same root with tricky prose round-trip", func(t *testing.T) {
		obj := yson.Object{
			"note": textOf("see Int(3) in figure (2]"),
			"body": yson.Tree{
				Root: yson.TreeNode{
					Type: "p",
					Children: []yson.TreeNode{
						{Type: "text", Value: "Tree(node) with ) brace }"},
					},
				},
			},
		}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		actual := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Equal(t, obj, actual)
	})

	t.Run("hand-written YSON literal with parens in string", func(t *testing.T) {
		input := `Text([{"val":"see figure (1)"}])`
		text := yson.Text{}
		assert.NoError(t, yson.Unmarshal(input, &text))
		assert.Equal(t, textOf("see figure (1)"), text)
	})

	t.Run("deeply nested tree past four levels round-trip", func(t *testing.T) {
		// depth: root > a > b > c > d > text
		leaf := yson.TreeNode{Type: "text", Value: "deep )"}
		d := yson.TreeNode{Type: "d", Children: []yson.TreeNode{leaf}}
		c := yson.TreeNode{Type: "c", Children: []yson.TreeNode{d}}
		b := yson.TreeNode{Type: "b", Children: []yson.TreeNode{c}}
		a := yson.TreeNode{Type: "a", Children: []yson.TreeNode{b}}
		tree := yson.Tree{Root: yson.TreeNode{
			Type: "root", Children: []yson.TreeNode{a},
		}}

		marshalled, err := tree.Marshal()
		assert.NoError(t, err)

		actual := yson.Tree{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Equal(t, tree, actual)
	})

	t.Run("counter regressions", func(t *testing.T) {
		intCounter := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(`Counter(Int(10))`, &intCounter))
		assert.Equal(t, crdt.IntegerCnt, intCounter.Type)
		assert.Equal(t, int32(10), intCounter.Value)

		longCounter := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(`Counter(Long(100))`, &longCounter))
		assert.Equal(t, crdt.LongCnt, longCounter.Type)
		assert.Equal(t, int64(100), longCounter.Value)

		dedup := yson.Counter{}
		assert.NoError(t, yson.Unmarshal(`DedupCounter(Int(15),"AQIDBA==")`, &dedup))
		assert.Equal(t, crdt.IntegerDedupCnt, dedup.Type)
		assert.Equal(t, int32(15), dedup.Value)
		assert.Equal(t, []byte{1, 2, 3, 4}, dedup.Registers)
	})

	t.Run("empty text and tree regressions", func(t *testing.T) {
		text := yson.Text{}
		assert.NoError(t, yson.Unmarshal(`Text()`, &text))
		assert.Equal(t, yson.Text{}, text)

		tree := yson.Tree{}
		assert.NoError(t, yson.Unmarshal(`Tree()`, &tree))
		assert.Equal(t, yson.Tree{Root: yson.TreeNode{Type: yson.DefaultRootNodeType}}, tree)
	})

	t.Run("scalar type regressions", func(t *testing.T) {
		arr := yson.Array{}
		assert.NoError(t, yson.Unmarshal(
			`[Int(42),Long(64),Date("2025-01-02T15:04:05.058Z"),BinData("AQID")]`, &arr))
		assert.Equal(t, int32(42), arr[0])
		assert.Equal(t, int64(64), arr[1])
		assert.Equal(t, gotime.Date(2025, 1, 2, 15, 4, 5, 58000000, gotime.UTC), arr[2])
		assert.Equal(t, []byte{1, 2, 3}, arr[3])
	})

	t.Run("malformed input returns invalid YSON without panic", func(t *testing.T) {
		obj := yson.Object{}
		assert.Error(t, yson.Unmarshal(`Text([{"val":"unterminated`, &obj))
		assert.Error(t, yson.Unmarshal(`Counter(Int(10)`, &obj))
	})

	t.Run("deeply nested constructors are rejected without exhausting the stack", func(t *testing.T) {
		// Real YSON nests constructors at most two deep (Counter(Int(...))).
		// A pathological input nesting far past that must return invalid YSON
		// rather than recursing until the goroutine stack is exhausted, which
		// would crash the process. The depth is chosen well above the parser's
		// bound so the preprocessing pass, not a later stage, rejects it.
		const depth = 100000
		data := `{"c":` + strings.Repeat("Int(", depth) + "0" +
			strings.Repeat(")", depth) + "}"

		obj := yson.Object{}
		assert.Error(t, yson.Unmarshal(data, &obj))
	})
}

// assertInvalidYSON asserts that err reports invalid YSON and names the
// constructor at fault, without dereferencing a nil error on failure.
func assertInvalidYSON(t *testing.T, err error, ctor string) {
	t.Helper()
	if !assert.Error(t, err) {
		return
	}
	assert.ErrorIs(t, err, yson.ErrInvalidYSON)
	if ctor != "" {
		assert.Contains(t, err.Error(), ctor)
	}
}

func TestYSONConstructorArgumentValidation(t *testing.T) {
	t.Run("extra argument is rejected instead of changing the type", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int(42,"type":"Long")}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("extra argument is rejected instead of being dropped", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Date("2025-01-01T00:00:00Z","junk":1)}`, &obj)
		assertInvalidYSON(t, err, "Date")
	})

	t.Run("unbalanced brace in an argument cannot invent a parent key", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int(1},"y":{"a":2)}`, &obj)
		assertInvalidYSON(t, err, "")
		assert.NotContains(t, obj, "y")
	})

	t.Run("unbalanced bracket in an argument cannot invent a parent key", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Text([{"val":"a"}]],"y":[{"a":2)}`, &obj)
		assertInvalidYSON(t, err, "")
		assert.NotContains(t, obj, "y")
	})

	t.Run("mismatched bracket kinds in an argument are rejected", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int([1})}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("missing argument is rejected with the constructor name", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int()}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("two arguments are rejected with the constructor name", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int(1,2)}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("Counter with an extra argument is rejected", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Counter(Int(1),"z")}`, &obj)
		assertInvalidYSON(t, err, "Counter")
	})

	t.Run("unterminated string in an argument is rejected", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Date("2025-01-01T00:00:00Z)}`, &obj)
		assertInvalidYSON(t, err, "Date")
	})

	t.Run("malformed DedupCounter is rejected with the constructor name", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":DedupCounter(Int(1),"x"},"y":{"a":1)}`, &obj)
		assertInvalidYSON(t, err, "DedupCounter")
		assert.NotContains(t, obj, "y")
	})

	t.Run("empty Text and Tree remain valid", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"t":Text(),"r":Tree()}`, &obj))
		assert.Equal(t, yson.Text{}, obj["t"])
	})

	t.Run("commas inside nested brackets are not arguments", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"t":Text([{"val":"a"},{"val":"b"}])}`, &obj))
		assert.Equal(t, yson.Text{Nodes: []yson.TextNode{{Value: "a"}, {Value: "b"}}}, obj["t"])
	})

	t.Run("commas inside string literals are not arguments", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"t":Text([{"val":"a,b"}])}`, &obj))
		assert.Equal(t, textOf("a,b"), obj["t"])
	})

	t.Run("parens inside string literals are not structure", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"t":Text([{"val":"a)b(c"}])}`, &obj))
		assert.Equal(t, textOf("a)b(c"), obj["t"])
	})

	t.Run("nested constructors still parse", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"c":Counter(Long(10))}`, &obj))
		assert.Equal(t, yson.Counter{Type: crdt.LongCnt, Value: int64(10)}, obj["c"])
	})

	t.Run("well-formed DedupCounter still parses", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal(`{"c":DedupCounter(Int(15),"aGVsbG8=")}`, &obj))
	})

	t.Run("DedupCounter registers must be base64", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":DedupCounter(Int(1),"a\")}`, &obj)
		assertInvalidYSON(t, err, "DedupCounter")
	})

	t.Run("DedupCounter with no registers round-trips", func(t *testing.T) {
		obj := yson.Object{"c": yson.Counter{
			Type:  crdt.IntegerDedupCnt,
			Value: int32(3),
		}}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		actual := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Equal(t, obj, actual)
	})

	t.Run("extra argument inside a nested constructor is rejected", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"c":Counter(Int(1,2))}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("an argument list of only a comma is rejected", func(t *testing.T) {
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":Int(,)}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("deeply nested constructors are rejected with the constructor name", func(t *testing.T) {
		deep := strings.Repeat("Int(", 200) + "0" + strings.Repeat(")", 200)
		obj := yson.Object{}
		err := yson.Unmarshal(`{"v":`+deep+`}`, &obj)
		assertInvalidYSON(t, err, "Int")
	})

	t.Run("constructor-like text in a string value is not a call site", func(t *testing.T) {
		values := []string{
			`DedupCounter(bogus`,
			`DedupCounter(Int(1),"x"},"y":{"a":1)`,
			`Int(42,"type":"Long")`,
			`Int(1},"y":{"a":2)`,
		}
		for _, v := range values {
			obj := yson.Object{"c": textOf(v)}
			marshalled, err := obj.Marshal()
			assert.NoError(t, err)

			actual := yson.Object{}
			assert.NoError(t, yson.Unmarshal(marshalled, &actual), "value %q", v)
			assert.Equal(t, obj, actual, "value %q", v)
		}
	})

	t.Run("whitespace around an argument is accepted", func(t *testing.T) {
		obj := yson.Object{}
		assert.NoError(t, yson.Unmarshal("{\"v\":Int( 42 ),\"c\":Counter(\n\tLong(3)\n)}", &obj))
		assert.Equal(t, int32(42), obj["v"])
		assert.Equal(t, yson.Counter{Type: crdt.LongCnt, Value: int64(3)}, obj["c"])
	})

	t.Run("text node attributes with commas round-trip", func(t *testing.T) {
		obj := yson.Object{"c": yson.Text{Nodes: []yson.TextNode{
			{Value: "a,b", Attributes: map[string]string{"k,1": "v,1", "k2": "v2"}},
		}}}
		marshalled, err := obj.Marshal()
		assert.NoError(t, err)

		actual := yson.Object{}
		assert.NoError(t, yson.Unmarshal(marshalled, &actual))
		assert.Equal(t, obj, actual)
	})

	t.Run("top-level array of constructors still parses", func(t *testing.T) {
		arr := yson.Array{}
		assert.NoError(t, yson.Unmarshal(`[Int(1),Text([{"val":"a"}]),Tree()]`, &arr))
		assert.Len(t, arr, 3)
	})
}
