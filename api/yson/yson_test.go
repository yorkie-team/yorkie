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
	"fmt"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/yson"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

func TestYSONConversion(t *testing.T) {

	t.Run("json struct conversion", func(t *testing.T) {
		doc := document.New("yson")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// an object and primitive types
			root.SetNewObject("k1").
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
			root.SetNewArray("k2").
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
			root.SetNewText("k3").
				Edit(0, 0, "ㅎ").
				Edit(0, 1, "하").
				Edit(0, 1, "한").
				Edit(0, 1, "하").
				Edit(1, 1, "느").
				Edit(1, 2, "늘").
				Edit(2, 2, "구름").
				Edit(2, 3, "뭉게구")

			// rich text
			root.SetNewText("k4").
				Edit(0, 0, "Hello world", nil).
				Edit(6, 11, "sky", nil).
				Style(0, 5, map[string]string{"b": "1"})

			// long counter
			root.SetNewCounter("k5", crdt.LongCnt, 0).
				Increase(10)

			// integer counter
			root.SetNewCounter("k6", crdt.IntegerCnt, 0).
				Increase(10)

			// tree
			root.SetNewTree("k7").
				Edit(0, 0, &json.TreeNode{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "Hello world",
					}},
				}, 0)

			return nil
		})
		assert.NoError(t, err)

		ysonStruct, err := yson.ToYSON(doc.RootObject())
		assert.NoError(t, err)

		newDoc := document.New("yson")
		err = newDoc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetFromYSON(ysonStruct)
			return nil
		})
		assert.NoError(t, err)

		// verify the conversion
		assert.Equal(t, doc.Marshal(), newDoc.Marshal())
		newYsonStruct, err := yson.ToYSON(newDoc.RootObject())
		assert.NoError(t, err)
		assert.Equal(t, ysonStruct.ToTestString(), newYsonStruct.ToTestString())
	})

	t.Run("array with nested types test", func(t *testing.T) {
		doc := document.New("nested-types")

		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.SetNewArray("nested")

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
			arr.AddNewTree(&json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{
					{Type: "text", Value: "Tree in array"},
					{
						Type: "span",
						Attributes: map[string]string{
							"style": "color: red",
						},
						Children: []json.TreeNode{
							{Type: "text", Value: "Styled text"},
						},
					},
				},
			})

			return nil
		})
		assert.NoError(t, err)

		// Convert to YSON
		ysonStruct, err := yson.ToYSON(doc.RootObject())
		assert.NoError(t, err)

		// Convert back to document
		newDoc := document.New("nested-types")
		err = newDoc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetFromYSON(ysonStruct)
			return nil
		})
		assert.NoError(t, err)

		// Verify the conversion
		assert.Equal(t, doc.Marshal(), newDoc.Marshal())
		newYsonStruct, err := yson.ToYSON(newDoc.RootObject())
		assert.NoError(t, err)
		assert.Equal(t, ysonStruct.ToTestString(), newYsonStruct.ToTestString())
	})

	t.Run("literal YSON conversion test", func(t *testing.T) {
		ysonStruct := &yson.Object{
			Value: map[string]yson.YSON{
				"nested": &yson.Array{
					Value: []yson.YSON{
						&yson.Array{
							Value: []yson.YSON{
								&yson.Primitive{
									ValueType: crdt.String,
									Value:     "nested1",
								},
								&yson.Primitive{
									ValueType: crdt.Integer,
									Value:     int32(42),
								},
							},
						},
						&yson.Object{
							Value: map[string]yson.YSON{
								"counter": &yson.Counter{
									ValueType: crdt.IntegerCnt,
									Value:     int32(10),
								},
								"key": &yson.Primitive{
									ValueType: crdt.String,
									Value:     "value",
								},
							},
						},
						&yson.Text{
							Value: `[{"attrs":{"bold":"true"},"val":"Hello"},{"val":" World"}]`,
						},
						&yson.Tree{
							Value: `{"type":"p","children":[{"type":"text","value":"Tree in array"},` +
								`{"type":"span","children":[{"type":"text","value":"Styled text"}],"attributes":{"style":"color: red"}}]}`,
						},
					},
				},
			},
		}

		doc := document.New("literal-ysonstruct")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetFromYSON(ysonStruct)
			return nil
		})
		assert.NoError(t, err)
		newYsonStruct, err := yson.ToYSON(doc.RootObject())
		assert.NoError(t, err)
		assert.Equal(t, ysonStruct, newYsonStruct)
	})

	t.Run("unsupported primitive type", func(t *testing.T) {
		doc := document.New("unsupported-primitive")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			defer func() {
				r := recover()
				assert.NotNil(t, r)
				errStr := fmt.Sprintf("%v", r)
				assert.Contains(t, errStr, "unsupported primitive type")
			}()

			root.SetFromYSON(&yson.Object{
				Value: map[string]yson.YSON{
					"key": &yson.Primitive{
						ValueType: 2000000,
						Value:     "value",
					},
				},
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{}`, doc.Marshal())
	})

	t.Run("invalid text JSON", func(t *testing.T) {
		doc := document.New("invalid-json")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			defer func() {
				r := recover()
				assert.NotNil(t, r)
				errStr := fmt.Sprintf("%v", r)
				assert.Contains(t, errStr, "failed to parse text JSON")
			}()

			text := root.SetNewText("text")
			text.EditFromYSON(yson.Text{
				Value: "invalid json",
			})
			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `{"text":[]}`, doc.Marshal())
	})

	t.Run("invalid tree JSON", func(t *testing.T) {
		doc := document.New("invalid-tree")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			treeNode, err := json.GetTreeRootNodeFromYSON(yson.Tree{
				Value: "invalid json",
			})
			if err != nil {
				return err
			}
			root.SetNewTree("tree", treeNode)
			return nil
		})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse tree JSON")
		assert.Equal(t, `{}`, doc.Marshal())
	})
}
