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
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/index"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestTree(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("tree", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. Create a tree and insert a paragraph.
			root.SetNewTree("t").Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{},
			}, 0)
			assert.Equal(t, "<root><p></p></root>", root.GetTree("t").ToXML())
			assert.Equal(t, `{"t":{"type":"root","children":[{"type":"p","children":[]}]}}`, root.Marshal())

			// 02. Create a text into the paragraph.
			root.GetTree("t").Edit(1, 1, &json.TreeNode{
				Type:  "text",
				Value: "AB",
			}, 0)
			assert.Equal(t, "<root><p>AB</p></root>", root.GetTree("t").ToXML())
			assert.Equal(
				t,
				`{"t":{"type":"root","children":[{"type":"p","children":[{"type":"text","value":"AB"}]}]}}`,
				root.Marshal(),
			)

			// 03. Insert a text into the paragraph.
			root.GetTree("t").Edit(3, 3, &json.TreeNode{
				Type:  "text",
				Value: "CD",
			}, 0)
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
			root.GetTree("t").Edit(1, 5, &json.TreeNode{
				Type:  "text",
				Value: "Yorkie",
			}, 0)
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
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
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

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("created from JSON with attributes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
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
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 1, &json.TreeNode{
				Type:  "text",
				Value: "X",
			}, 0)
			assert.Equal(t, "<doc><p>Xab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil, 0)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "X",
			}, 0)
			assert.Equal(t, "<doc><p>aXb</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil, 0)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "<doc><p>ab</p></doc>", doc.Root().GetTree("t").ToXML())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "X",
			}, 0)
			assert.Equal(t, "<doc><p>aXb</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 4, nil, 0)
			assert.Equal(t, "<doc><p></p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, "<doc><p></p></doc>", doc.Root().GetTree("t").ToXML())

		err = doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 3, &json.TreeNode{
				Type:  "text",
				Value: "X",
			}, 0)
			assert.Equal(t, "<doc><p>abX</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 4, nil, 0)
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil, 0)
			assert.Equal(t, "<doc><p>a</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit content with path test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
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

			root.GetTree("t").EditByPath([]int{0, 0, 0, 1}, []int{0, 0, 0, 1}, &json.TreeNode{
				Type:  "text",
				Value: "X",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 0, 3}, []int{0, 0, 0, 3}, &json.TreeNode{
				Type:  "text",
				Value: "!",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 1}, []int{0, 0, 1}, &json.TreeNode{
				Type: "tn",
				Children: []json.TreeNode{{
					Type: "text", Value: "cd",
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn><tn>cd</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 1}, []int{0, 1}, &json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{{
						Type: "text", Value: "q",
					}},
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn><tn>cd</tn></p><p><tn>q</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 1, 0, 0}, []int{0, 1, 0, 0}, &json.TreeNode{
				Type:  "text",
				Value: "a",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn><tn>cd</tn></p><p><tn>aq</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 1, 0, 2}, []int{0, 1, 0, 2}, &json.TreeNode{
				Type:  "text",
				Value: "B",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXb!</tn><tn>cd</tn></p><p><tn>aqB</tn></p></tc></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditByPath([]int{0, 0, 4}, []int{0, 0, 4}, &json.TreeNode{
						Type:     "tn",
						Children: []json.TreeNode{},
					}, 0)
					return nil
				})
			}, index.ErrUnreachablePath)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit content with path test 2", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "tc",
					Children: []json.TreeNode{{
						Type: "p", Children: []json.TreeNode{{
							Type: "tn", Children: []json.TreeNode{},
						}},
					}},
				}},
			})
			assert.Equal(t, "<doc><tc><p><tn></tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 0, 0, 0}, []int{0, 0, 0, 0}, &json.TreeNode{
				Type:  "text",
				Value: "a",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 1}, []int{0, 1}, &json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{},
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn></tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 1, 0, 0}, []int{0, 1, 0, 0}, &json.TreeNode{
				Type:  "text",
				Value: "b",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 2}, []int{0, 2}, &json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{},
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p><p><tn></tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 2, 0, 0}, []int{0, 2, 0, 0}, &json.TreeNode{
				Type:  "text",
				Value: "c",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p><p><tn>c</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 3}, []int{0, 3}, &json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{},
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p><p><tn>c</tn></p><p><tn></tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 3, 0, 0}, []int{0, 3, 0, 0}, &json.TreeNode{
				Type:  "text",
				Value: "d",
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p><p><tn>c</tn></p><p><tn>d</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditByPath([]int{0, 3}, []int{0, 3}, &json.TreeNode{
				Type: "p",
				Children: []json.TreeNode{{
					Type: "tn", Children: []json.TreeNode{},
				}},
			}, 0)
			assert.Equal(t, "<doc><tc><p><tn>a</tn></p><p><tn>b</tn></p><p><tn>c</tn></p><p><tn></tn></p><p><tn>d</tn></p></tc></doc>", root.GetTree("t").ToXML())
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("sync its content with other clients test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "hello"}},
				}},
			})
			assert.Equal(t, "<doc><p>hello</p></doc>", root.GetTree("t").ToXML())
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.Equal(t, "<doc><p>hello</p></doc>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<doc><p>hello</p></doc>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(7, 7, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "yorkie"}},
			}, 0)
			return nil
		}))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<doc><p>hello</p><p>yorkie</p></doc>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("insert multiple text nodes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
				Type:  "text",
				Value: "c",
			}, &json.TreeNode{
				Type:  "text",
				Value: "d",
			}}, 0)
			assert.Equal(t, "<doc><p>abcd</p></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("insert multiple element nodes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditBulk(4, 4, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "cd"}},
			}, &json.TreeNode{
				Type:     "i",
				Children: []json.TreeNode{{Type: "text", Value: "fg"}},
			}}, 0)
			assert.Equal(t, "<doc><p>ab</p><p>cd</p><i>fg</i></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content with path when multi tree nodes passed", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
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

			root.GetTree("t").EditBulkByPath([]int{0, 0, 0, 1}, []int{0, 0, 0, 1}, []*json.TreeNode{{
				Type:  "text",
				Value: "X",
			}, {
				Type:  "text",
				Value: "X",
			}}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXXb</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditBulkByPath([]int{0, 1}, []int{0, 1}, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "te"}, {Type: "text", Value: "st"}}}},
			}, {
				Type:     "p",
				Children: []json.TreeNode{{Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "te"}, {Type: "text", Value: "xt"}}}},
			}}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXXb</tn></p><p><tn>test</tn></p><p><tn>text</tn></p></tc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").EditBulkByPath([]int{0, 3}, []int{0, 3}, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "te"}, {Type: "text", Value: "st"}}}},
			}, {
				Type: "tn", Children: []json.TreeNode{{Type: "text", Value: "te"}, {Type: "text", Value: "xt"}},
			}}, 0)
			assert.Equal(t, "<doc><tc><p><tn>aXXb</tn></p><p><tn>test</tn></p><p><tn>text</tn></p><p><tn>test</tn></p><tn>text</tn></tc></doc>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("detecting error for empty text test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "ab",
					}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
						Type:  "text",
						Value: "c",
					}, {
						Type:  "text",
						Value: "",
					}}, 0)
					return nil
				})
			}, json.ErrEmptyTextNode)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("detecting error for mixed type insertion test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "ab",
					}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{},
					}, {
						Type:  "text",
						Value: "d",
					}}, 0)
					return nil
				})
			}, json.ErrMixedNodeType)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("detecting correct error order test 1", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "ab",
					}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: "c"}, {Type: "text", Value: ""}},
					}, {
						Type: "text", Value: "d",
					}}, 0)
					return nil
				})
			}, json.ErrMixedNodeType)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("detecting correct error order test 2", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "ab",
					}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: "c"}},
					}, {
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: ""}},
					}}, 0)
					return nil
				})
			}, json.ErrEmptyTextNode)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("detecting correct error order test 3", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:  "text",
						Value: "ab",
					}},
				}},
			})
			assert.Equal(t, "<doc><p>ab</p></doc>", root.GetTree("t").ToXML())

			assert.Panics(t, func() {
				doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{
						Type:  "text",
						Value: "d",
					}, {
						Type:     "p",
						Children: []json.TreeNode{{Type: "text", Value: "c"}},
					}}, 0)
					return nil
				})
			}, json.ErrMixedNodeType)
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("edit its content with attributes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{Type: "doc"})
			assert.Equal(t, "<doc></doc>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(0, 0, &json.TreeNode{
				Type:       "p",
				Attributes: map[string]string{"bold": "true"},
				Children:   []json.TreeNode{{Type: "text", Value: "ab"}},
			}, 0)
			assert.Equal(t, `<doc><p bold="true">ab</p></doc>`, root.GetTree("t").ToXML())

			root.GetTree("t").Edit(4, 4, &json.TreeNode{
				Type:       "p",
				Attributes: map[string]string{"italic": "true"},
				Children:   []json.TreeNode{{Type: "text", Value: "cd"}},
			}, 0)
			assert.Equal(t, `<doc><p bold="true">ab</p><p italic="true">cd</p></doc>`, root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 6, nil, 0)
			assert.Equal(t, `<doc><p bold="true">ad</p></doc>`, root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
		assert.Equal(t, `<doc><p bold="true">ad</p></doc>`, doc.Root().GetTree("t").ToXML())
	})

	t.Run("set attributes test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			// NOTE(sejongk): 0, 4 -> 0,1 / 3,4
			root.GetTree("t").Style(0, 4, map[string]string{"bold": "true"})
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

	// Concurrent editing, overlapping range test
	t.Run("concurrently delete overlapping elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}, {
					Type:     "i",
					Children: []json.TreeNode{},
				}, {
					Type:     "b",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p></p><i></i><b></b></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 6, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><b></b></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete overlapping text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>abcd</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 5, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>d</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	// Concurrent editing, contained range test
	t.Run("concurrently insert and delete contained elements of the same depth test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p><p>abcd</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(6, 6, &json.TreeNode{Type: "p", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 12, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>1234</p><p></p><p>abcd</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently multiple insert and delete contained elements of the same depth test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "abcd"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p><p>abcd</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(6, 6, &json.TreeNode{Type: "p", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(8, 8, &json.TreeNode{Type: "p", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(10, 10, &json.TreeNode{Type: "p", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 12, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>1234</p><p></p><p></p><p></p><p>abcd</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p><p></p><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p><p></p><p></p></root>", d2.Root().GetTree("t").ToXML())
	})

	t.Run("detecting error when inserting and deleting contained elements at different depths test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "i",
						Children: []json.TreeNode{},
					}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p><i></i></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p><i><i></i></i></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete contained elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "i",
						Children: []json.TreeNode{{Type: "text", Value: "1234"}},
					}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p><i>1234</i></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 8, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 7, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert and delete contained text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 5, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12a34</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>a</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete contained text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 5, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>14</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert and delete contained text and elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 6, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12a34</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete contained text and elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 6, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 5, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
	})

	// Concurrent editing, side by side range test
	t.Run("concurrently insert side by side elements (left) test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 0, &json.TreeNode{Type: "b", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 0, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><b></b><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><i></i><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><i></i><b></b><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert side by side elements (middle) test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "b", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p><b></b></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p><i></i></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p><i></i><b></b></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert side by side elements (right) test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "b", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p></p><b></b></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p><i></i></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p><i></i><b></b></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert and delete side by side elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "b",
						Children: []json.TreeNode{},
					}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p><b></b></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p><i></i><b></b></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p><i></i></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete and insert side by side elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "b",
						Children: []json.TreeNode{},
					}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p><b></b></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "i", Children: []json.TreeNode{}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p><b></b><i></i></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p><i></i></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete side by side elements test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type: "p",
					Children: []json.TreeNode{{
						Type:     "b",
						Children: []json.TreeNode{},
					}, {
						Type:     "i",
						Children: []json.TreeNode{},
					}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p><b></b><i></i></p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p><i></i></p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p><b></b></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("insert text to the same position(left) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "B"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>A12</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>B12</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>BA12</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("insert text to the same position(middle) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "B"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>1A2</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>1B2</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>1BA2</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("insert text content to the same position(right) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "B"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12A</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12B</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>12BA</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently insert and delete side by side text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12a34</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>12a</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete and insert side by side text test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "a"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12a34</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>34</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>a34</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("concurrently delete side by side text blocks test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 5, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>34</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("delete text content at the same position(left) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "123"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>123</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>23</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>23</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>23</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("delete text content at the same position(middle) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "123"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>123</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>13</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>13</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>13</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("delete text content at the same position(right) concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "123"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>123</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 4, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>12</p></root>", d1.Root().GetTree("t").ToXML())
	})

	// Concurrent editing, complex cases test
	t.Run("delete text content anchored to another concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "123"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>123</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>23</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>13</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>3</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("produce complete deletion concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "123"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>123</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>23</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>1</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p></p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle block delete concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12345"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12345</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 6, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>345</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>123</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>3</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle insert within block delete concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12345"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12345</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 5, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "B"}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>15</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12B345</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>1B5</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle insert within block delete concurrently test 2", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12345"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12345</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 6, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditBulk(3, 3, []*json.TreeNode{{Type: "text", Value: "a"}, {Type: "text", Value: "bc"}}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>1</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12abc345</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>1abc</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle block element insertion within delete test 2", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "1234"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "5678"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>1234</p><p>5678</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 12, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditBulk(6, 6, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "cd"}},
			}, {
				Type:     "i",
				Children: []json.TreeNode{{Type: "text", Value: "fg"}},
			}}, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>1234</p><p>cd</p><i>fg</i><p>5678</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>cd</p><i>fg</i></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle concurrent element insert/deletion (left) test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12345"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12345</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 7, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditBulk(0, 0, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "cd"}},
			}, {
				Type:     "i",
				Children: []json.TreeNode{{Type: "text", Value: "fg"}},
			}}, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>cd</p><i>fg</i><p>12345</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>cd</p><i>fg</i></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle concurrent element insert/deletion (right) test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "12345"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "<root><p>12345</p></root>", d1.Root().GetTree("t").ToXML())

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(0, 7, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").EditBulk(7, 7, []*json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "cd"}},
			}, {
				Type:     "i",
				Children: []json.TreeNode{{Type: "text", Value: "fg"}},
			}}, 0)
			return nil
		}))
		assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>12345</p><p>cd</p><i>fg</i></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>cd</p><i>fg</i></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle deletion of insertion anchor concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 2, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>1A2</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>2</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>A2</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle deletion after insertion concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 1, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>A12</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>A</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("handle deletion before insertion concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{Type: "text", Value: "A"}, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(1, 3, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>12A</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p></p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>A</p></root>", d1.Root().GetTree("t").ToXML())
	})

	// Edge cases test
	t.Run("delete very first text when there is tombstone in front of target text test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. Create a tree and insert a paragraph.
			root.SetNewTree("t").Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcdefghi"}}}, 0)
			assert.Equal(t, "<root><p>abcdefghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 1, &json.TreeNode{
				Type:  "text",
				Value: "12345",
			}, 0)
			assert.Equal(t, "<root><p>12345abcdefghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 5, nil, 0)
			assert.Equal(t, "<root><p>15abcdefghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 5, nil, 0)
			assert.Equal(t, "<root><p>15cdefghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 4, nil, 0)
			assert.Equal(t, "<root><p>1defghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 3, nil, 0)
			assert.Equal(t, "<root><p>efghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil, 0)
			assert.Equal(t, "<root><p>fghi</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 5, nil, 0)
			assert.Equal(t, "<root><p>f</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil, 0)
			assert.Equal(t, "<root><p></p></root>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("delete node when there is more than one text node in front which has size bigger than 1 test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			// 01. Create a tree and insert a paragraph.
			root.SetNewTree("t").Edit(0, 0, &json.TreeNode{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "abcde"}}}, 0)
			assert.Equal(t, "<root><p>abcde</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(6, 6, &json.TreeNode{
				Type:  "text",
				Value: "f",
			}, 0)
			assert.Equal(t, "<root><p>abcdef</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(7, 7, &json.TreeNode{
				Type:  "text",
				Value: "g",
			}, 0)
			assert.Equal(t, "<root><p>abcdefg</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(7, 8, nil, 0)
			assert.Equal(t, "<root><p>abcdef</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(6, 7, nil, 0)
			assert.Equal(t, "<root><p>abcde</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(5, 6, nil, 0)
			assert.Equal(t, "<root><p>abcd</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(4, 5, nil, 0)
			assert.Equal(t, "<root><p>abc</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(3, 4, nil, 0)
			assert.Equal(t, "<root><p>ab</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(2, 3, nil, 0)
			assert.Equal(t, "<root><p>a</p></root>", root.GetTree("t").ToXML())

			root.GetTree("t").Edit(1, 2, nil, 0)
			assert.Equal(t, "<root><p></p></root>", root.GetTree("t").ToXML())

			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("split link can transmitted through rpc", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			return nil
		}))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "1",
			}, 0)
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 3, &json.TreeNode{
				Type:  "text",
				Value: "1",
			}, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>a11b</p></root>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 3, &json.TreeNode{
				Type:  "text",
				Value: "12",
			}, 0)
			return nil
		}))

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 5, &json.TreeNode{
				Type:  "text",
				Value: "21",
			}, 0)
			return nil
		}))

		assert.Equal(t, "<root><p>a1221b</p></root>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, &json.TreeNode{
				Type:  "text",
				Value: "123",
			}, 0)
			return nil
		}))

		assert.Equal(t, "<root><p>a12321b</p></root>", d2.Root().GetTree("t").ToXML())
	})

	t.Run("can calculate size of index tree correctly", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", &json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})
			return nil
		}))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "123",
			}, 0)
			return nil
		}))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "456",
			}, 0)
			return nil
		}))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "789",
			}, 0)
			return nil
		}))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 2, &json.TreeNode{
				Type:  "text",
				Value: "0123",
			}, 0)
			return nil
		}))

		assert.Equal(t, "<root><p>a0123789456123b</p></root>", d1.Root().GetTree("t").ToXML())
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		size := d1.Root().GetTree("t").IndexTree.Root().Len()
		assert.Equal(t, size, d2.Root().GetTree("t").IndexTree.Root().Len())
	})
}
