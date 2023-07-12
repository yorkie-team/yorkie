//go:build integration

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestTree(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("tree", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			// 01. Create a tree and insert a paragraph.
			root.SetNewTree("t").Edit(0, 0, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{},
			}})
			assert.Equal(t, "<root><p></p></root>", root.GetTree("t").ToXML())
			assert.Equal(t, `{"t":{"type":"root","children":[{"type":"p","children":[]}]}}`, root.Marshal())

			// 02. Create a text into the paragraph.
			root.GetTree("t").Edit(1, 1, []*json.TreeNode{{
				Type:  "text",
				Value: "AB",
			}})
			assert.Equal(t, "<root><p>AB</p></root>", root.GetTree("t").ToXML())
			assert.Equal(
				t,
				`{"t":{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"AB"}]}]}}`,
				root.Marshal(),
			)

			// 03. Insert a text into the paragraph.
			root.GetTree("t").Edit(3, 3, []*json.TreeNode{{
				Type:  "text",
				Value: "CD",
			}})
			assert.Equal(t, "<root><p>ABCD</p></root>", root.GetTree("t").ToXML())

			// TODO(krapie): consider other options to avoid line over
			text1 := `{"t":{"type":"root","children":[{"type":"p","children"`
			text2 := `:[{"type":"text","value":"AB"},{"type":"text","value":"CD"}]}]}}`
			assert.Equal(
				t,
				text1+text2,
				root.Marshal(),
			)

			// 04. Replace ABCD with Yorkie
			root.GetTree("t").Edit(1, 5, []*json.TreeNode{{
				Type:  "text",
				Value: "Yorkie",
			}})
			assert.Equal(t, "<root><p>Yorkie</p></root>", root.GetTree("t").ToXML())
			assert.Equal(
				t,
				`{"t":{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"Yorkie"}]}]}}`,
				root.Marshal(),
			)

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("created from JSON test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}, {
					Type: "ng",
					Children: []json.TreeNode{
						{Type: "note", Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
						{Type: "note", Children: []json.TreeNode{{Type: "text", Value: "ef"}}},
					},
				}, {
					Type:     "bp",
					Children: []json.TreeNode{{Type: "text", Value: "gh"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p><ng><note>cd</note><note>ef</note></ng><bp>gh</bp></doc>", root.GetTree("t").ToXML())
			assert.Equal(t, 18, root.GetTree("t").Len())
			// TODO(krapie): add listEqual test later
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("created from JSON with attributes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:       "span",
						Attributes: map[string]string{"bold": "true"},
						Children:   []json.TreeNode{{Type: "text", Value: "hello"}},
					}},
				}},
			})
			assert.Equal(t, `<doc><p><span bold="true">hello</span></p></doc>`, root.GetTree("t").ToXML())
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 1, []*json.TreeNode{{
				Type:  "text",
				Value: "X",
			}})
			assert.Equal(t, "<doc><p>Xab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 2, []*json.TreeNode{{
				Type:  "text",
				Value: "X",
			}})
			assert.Equal(t, "<doc><p>aXb</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "<doc><p>ab</p></doc>", doc.Root().GetTree("t").ToXML())

		err = doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 3, []*json.TreeNode{{
				Type:  "text",
				Value: "X",
			}})
			assert.Equal(t, "<doc><p>abX</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 4, nil)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil)
			assert.Equal(t, "<doc><p>a</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content with path", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "tc",
					Children: []json.TreeNode{{
						Type: "p", Children: []json.TreeNode{{
							Type: "tn", Children: []json.TreeNode{{
								Type: "text", Value: "ab",
							}},
						}},
					}},
				}},
			})
			assert.Equal(t, "<doc><tc><p><tn>ab</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 0, 1}, []int{0, 0, 0, 1}, []*json.TreeNode{{
				Type:  "text",
				Value: "X",
			}})
			assert.Equal(t, "<doc><tc><p><tn>aXb</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 0, 3}, []int{0, 0, 0, 3}, []*json.TreeNode{{
				Type:  "text",
				Value: "!",
			}})
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, []*json.TreeNode{{
				Type:     "tn",
				Children: []json.TreeNode{},
			}})
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn><tn></tn></p></tc></doc>", root.GetTree("t").ToXML())
			return nil
		})
		assert.NoError(t, err)
	})
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content with attributes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{Type: "doc"})
			assert.Equal(t, "<doc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(0, 0, []*json.TreeNode{{
				Type:       "p",
				Attributes: map[string]string{"bold": "true"},
				Children:   []json.TreeNode{{Type: "text", Value: "ab"}},
			}})
			assert.Equal(t, `<doc><p bold="true">ab</p></doc>`, root.GetTree("t").ToXML())

			root.GetTree("t").Edit(4, 4, []*json.TreeNode{{
				Type:       "p",
				Attributes: map[string]string{"italic": "true"},
				Children:   []json.TreeNode{{Type: "text", Value: "cd"}},
			}})
			assert.Equal(t, `<doc><p bold="true">ab</p><p italic="true">cd</p></doc>`, root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 6, nil)
			assert.Equal(t, `<doc><p italic="true">ad</p></doc>`, root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `<doc><p italic="true">ad</p></doc>`, doc.Root().GetTree("t").ToXML())
	})

	t.Run("sync with other clients test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "Hello"}},
				}},
			})
			assert.Equal(t, "<root><p>Hello</p></root>", root.GetTree("t").ToXML())
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.Equal(t, "<root><p>Hello</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>Hello</p></root>", d2.Root().GetTree("t").ToXML())
	})

	t.Run("set attributes test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{
					{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}}},
					{Type: "p", Attributes: map[string]string{"italic": "true"}, Children: []json.TreeNode{{Type: "text", Value: "cd"}}},
				},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, `<root><p>ab</p><p italic="true">cd</p></root>`, d1.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetTree("t").Style(3, 4, map[string]string{"bold": "true"})
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.Equal(t, `<root><p bold="true">ab</p><p italic="true">cd</p></root>`, d1.Root().GetTree("t").ToXML())
		assert.Equal(t, `<root><p bold="true">ab</p><p italic="true">cd</p></root>`, d2.Root().GetTree("t").ToXML())

		assert.Equal(t, `{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"ab"}],"attributes":{"bold":"true"}},{"type":"p","children":[{"type":"text","value":"cd"}],"attributes":{"italic":"true"}}]}`, d1.Root().GetTree("t").Marshal())
		assert.Equal(t, `{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"ab"}],"attributes":{"bold":"true"}},{"type":"p","children":[{"type":"text","value":"cd"}],"attributes":{"italic":"true"}}]}`, d2.Root().GetTree("t").Marshal())
	})

	t.Run("insert inline content to the same position(left) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(1, 1, []*json.TreeNode{{Type: "text", Value: "A"}})
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(1, 1, []*json.TreeNode{{Type: "text", Value: "B"}})
			return nil
		}))
		assert.Equal(t, "<root><p>A12</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>B12</p></root>", d2.Root().GetTree("t").ToXML())

		t.Skip("TODO(krapie): find bug on concurrent insert inline content to the same position(left)")
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("insert inline content to the same position(middle) concurrently", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(2, 2, []*json.TreeNode{{Type: "text", Value: "A"}})
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(2, 2, []*json.TreeNode{{Type: "text", Value: "B"}})
			return nil
		}))
		assert.Equal(t, "<root><p>1A2</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>1B2</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("insert inline content to the same position(right) concurrently", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(3, 3, []*json.TreeNode{{Type: "text", Value: "A"}})
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object) error {
			root.GetTree("t").Edit(3, 3, []*json.TreeNode{{Type: "text", Value: "B"}})
			return nil
		}))
		assert.Equal(t, "<root><p>12A</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12B</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
