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
			r.SetNewCounter("k5", crdt.LongCnt, 0).
				Increase(10)

			// integer counter
			r.SetNewCounter("k6", crdt.IntegerCnt, 0).
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
			obj.SetNewCounter("counter", crdt.IntegerCnt, 10)

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
				expectedErr: "invalid character",
			},
			{
				name:        "unknown type",
				input:       `Unknown(123)`,
				targetType:  &yson.Object{},
				expectedErr: "invalid character 'U",
			},
			{
				name:        "invalid counter type",
				input:       `Counter(String("invalid"))`,
				targetType:  &yson.Counter{},
				expectedErr: "invalid character 'S",
			},
			{
				name:        "invalid tree format",
				input:       `Tree("not an object")`,
				targetType:  &yson.Tree{},
				expectedErr: "expected tree, got",
			},
			{
				name:        "invalid text format",
				input:       `Text("not an array")`,
				targetType:  &yson.Text{},
				expectedErr: "expected text, got",
			},
			{
				name:        "missing required field in text node",
				input:       `Text([{"attrs":{"bold":"true"}}])`,
				targetType:  &yson.Text{},
				expectedErr: "missing val field",
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
