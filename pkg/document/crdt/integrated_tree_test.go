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

package crdt_test

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

	t.Run("handle deletion after insertion concurrently test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
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

	t.Run("created from JSON with attributes test", func(t *testing.T) {
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
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
			root.SetNewTree("t", json.TreeNode{
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
			root.SetNewTree("t", json.TreeNode{
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
			root.SetNewTree("t", json.TreeNode{
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
			root.SetNewTree("t", json.TreeNode{
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
			root.SetNewTree("t", json.TreeNode{
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

	t.Run("overlapping-merge-and-delete-element-node", func(t *testing.T) {
		// t.Skip("remove this after supporting concurrent merge and split")
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "a"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "b"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, "<root><p>a</p><p>b</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p><p>b</p></root>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 6, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>ab</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>a</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("trial overlapping-merge-and-delete-element-node", func(t *testing.T) {
		t.Skip("remove this after supporting concurrent merge and split")
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "a"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "b"}, {Type: "text", Value: "c"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, "<root><p>a</p><p>bc</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p><p>bc</p></root>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(3, 7, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>abc</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>a</p></root>", d1.Root().GetTree("t").ToXML())
	})

	t.Run("overlapping-merge-and-delete-text-nodes", func(t *testing.T) {
		// t.Skip("remove this after supporting concurrent merge and split")
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{
				Type: "root",
				Children: []json.TreeNode{{
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "a"}},
				}, {
					Type:     "p",
					Children: []json.TreeNode{{Type: "text", Value: "bcde"}},
				}},
			})
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, "<root><p>a</p><p>bcde</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p><p>bcde</p></root>", d2.Root().GetTree("t").ToXML())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(2, 4, nil, 0)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetTree("t").Edit(4, 6, nil, 0)
			return nil
		}))
		assert.Equal(t, "<root><p>abcde</p></root>", d1.Root().GetTree("t").ToXML())
		assert.Equal(t, "<root><p>a</p><p>de</p></root>", d2.Root().GetTree("t").ToXML())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, "<root><p>ade</p></root>", d1.Root().GetTree("t").ToXML())
	})

	// Concurrent editing, contained range test

	// t.Run("contained-split-and-split-at-different-levels", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type: "p",
	// 				Children: []json.TreeNode{{
	// 					Type:     "p",
	// 					Children: []json.TreeNode{{Type: "text", Value: "ab"}},
	// 				}, {
	// 					Type:     "p",
	// 					Children: []json.TreeNode{{Type: "text", Value: "c"}},
	// 				}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p><p>ab</p><p>c</p></p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p><p>ab</p><p>c</p></p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(3, 3, nil, 1)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(5, 5, nil, 1)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p><p>a</p><p>b</p><p>c</p></p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p><p>ab</p></p><p><p>c</p></p></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root><p><p>a</p><p>b</p></p><p><p>c</p></p></root>", d1.Root().GetTree("t").ToXML())
	// })

	// t.Run("contained-split-and-delete-the-whole-original-and-split-nodes", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p>ab</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>ab</p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(2, 2, nil, 1)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(0, 4, nil, 0)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p>a</p><p>b</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root></root>", d1.Root().GetTree("t").ToXML())
	// })

	// t.Run("contained-merge-and-merge-at-the-same-level", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "a"}},
	// 			}, {
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "b"}},
	// 			}, {
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "c"}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p>a</p><p>b</p><p>c</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>a</p><p>b</p><p>c</p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(2, 7, nil, 0)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(5, 7, nil, 0)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p>ac</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>a</p><p>bc</p></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root><p>ac</p></root>", d1.Root().GetTree("t").ToXML())
	// })

	// t.Run("contained-merge-and-insert", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "a"}},
	// 			}, {
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "b"}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p>a</p><p>b</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>a</p><p>b</p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(2, 4, nil, 0)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(4, 4, &json.TreeNode{Type: "text", Value: "c"}, 0)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p>ab</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>a</p><p>cb</p></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root><p>acb</p></root>", d1.Root().GetTree("t").ToXML())
	// })

	// t.Run("side-by-side-split-and-insert", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p>ab</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>ab</p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(2, 2, nil, 1)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(4, 4, &json.TreeNode{
	// 			Type:     "p",
	// 			Children: []json.TreeNode{{Type: "text", Value: "c"}},
	// 		}, 0)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p>a</p><p>b</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>ab</p><p>c</p></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root><p>a</p><p>b</p><p>c</p></root>", d1.Root().GetTree("t").ToXML())
	// })

	// t.Run("side-by-side-split-and-delete", func(t *testing.T) {
	// 	t.Skip("remove this after supporting concurrent merge and split")
	// 	ctx := context.Background()
	// 	d1 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c1.Attach(ctx, d1))
	// 	d2 := document.New(helper.TestDocKey(t))
	// 	assert.NoError(t, c2.Attach(ctx, d2))

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.SetNewTree("t", json.TreeNode{
	// 			Type: "root",
	// 			Children: []json.TreeNode{{
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "ab"}},
	// 			}, {
	// 				Type:     "p",
	// 				Children: []json.TreeNode{{Type: "text", Value: "c"}},
	// 			}},
	// 		})
	// 		return nil
	// 	}))
	// 	assert.NoError(t, c1.Sync(ctx))
	// 	assert.NoError(t, c2.Sync(ctx))
	// 	assert.Equal(t, "<root><p>ab</p><p>c</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>ab</p><p>c</p></root>", d2.Root().GetTree("t").ToXML())

	// 	assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(2, 2, nil, 1)
	// 		return nil
	// 	}))
	// 	assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
	// 		root.GetTree("t").Edit(4, 7, nil, 0)
	// 		return nil
	// 	}))
	// 	assert.Equal(t, "<root><p>a</p><p>b</p><p>c</p></root>", d1.Root().GetTree("t").ToXML())
	// 	assert.Equal(t, "<root><p>ab</p></root>", d2.Root().GetTree("t").ToXML())

	// 	syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	// 	assert.Equal(t, "<root><p>a</p><p>b</p></root>", d1.Root().GetTree("t").ToXML())
	// })
}
