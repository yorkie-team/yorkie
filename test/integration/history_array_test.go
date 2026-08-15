//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// applyArrayOp1 applies the given operation to the "list" array using the
// first client's index pattern. It is the port of the JS test's applyOp1
// (history_array_test.ts).
func applyArrayOp1(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		list := root.GetArray("list")

		switch op {
		case "add":
			if list.Len() == 0 {
				return nil
			}
			list.InsertStringAfter(0, "insV")
		case "move":
			if list.Len() < 3 {
				return nil
			}
			list.MoveAfterByIndex(2, 0)
		case "remove":
			if list.Len() > 0 {
				list.Delete(0)
			}
		case "set":
			if list.Len() > 1 {
				list.SetString(1, "s")
			}
		}
		return nil
	}, op))
}

// applyArrayOp2 applies the given operation to the "list" array using the
// second client's index pattern. It is the port of the JS test's applyOp2
// (history_array_test.ts).
func applyArrayOp2(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		list := root.GetArray("list")

		switch op {
		case "add":
			if list.Len() <= 2 {
				return nil
			}
			list.InsertStringAfter(2, "insV")
		case "move":
			if list.Len() < 4 {
				return nil
			}
			list.MoveAfterByIndex(3, 1)
		case "remove":
			if list.Len() > 1 {
				list.Delete(1)
			}
		case "set":
			if list.Len() > 2 {
				list.SetString(2, "2")
			}
		}
		return nil
	}, op))
}

func TestHistoryArray(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	// describe('Array Undo Operations') - single-op undo.
	for _, op := range []string{"add", "move", "remove", "set"} {
		op := op
		t.Run(fmt.Sprintf("should handle undo of %s operation", op), func(t *testing.T) {
			doc := document.New(helper.TestKey(t))
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewArray("list").AddString("a", "b", "c", "d", "e")
				return nil
			}, "init"))

			initialJSON := doc.Marshal()

			applyArrayOp1(t, doc, op)
			assert.NoError(t, doc.Undo())

			assert.Equal(t, initialJSON, doc.Marshal(), "initial state - undo state")
		})
	}

	// describe('Array Undo Operations') - three-op sequences undone in reverse.
	ops := []string{"add", "remove", "move", "set"}
	for _, op1 := range ops {
		for _, op2 := range ops {
			for _, op3 := range ops {
				op1, op2, op3 := op1, op2, op3
				caseName := fmt.Sprintf("%s-%s-%s", op1, op2, op3)
				// KNOWN: the set operation inserts at the element's original
				// (dead) position for concurrent convergence, so undo
				// restores the value there instead of the moved position.
				// Fixing this requires a proto-level change. Ported as
				// skipped to match the JS SDK's it.skip for the same cases.
				skip := (op1 == "move" && op3 == "set") ||
					(op1 == "move" && op2 == "move" && op3 == "set")

				t.Run(fmt.Sprintf("should return to each state correctly: %s", caseName), func(t *testing.T) {
					if skip {
						t.Skip("KNOWN: set after move restores the dead position, not the moved one (proto-level change needed)")
					}

					doc := document.New(helper.TestKey(t))
					assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
						root.SetNewArray("list").AddString("a", "b", "c", "d", "e")
						return nil
					}, "init"))

					var s []string
					s = append(s, doc.Marshal())

					applyArrayOp1(t, doc, op1)
					s = append(s, doc.Marshal())

					applyArrayOp1(t, doc, op2)
					s = append(s, doc.Marshal())

					applyArrayOp1(t, doc, op3)
					s = append(s, doc.Marshal())

					for i := 3; i >= 1; i-- {
						assert.NoError(t, doc.Undo())
						back := doc.Marshal()
						assert.Equal(t, s[i-1], back, fmt.Sprintf("undo back to S%d failed on %s", i-1, caseName))
					}
				})
			}
		}
	}

	// describe('Array Undo in Multi-Client')
	for _, op1 := range ops {
		for _, op2 := range ops {
			op1, op2 := op1, op2
			caseName := fmt.Sprintf("%s-%s", op1, op2)
			t.Run(fmt.Sprintf("should handle multi user undo: %s", caseName), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				// Initial setup
				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewArray("list").AddString("a", "b", "c", "d", "e")
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal())

				applyArrayOp1(t, d1, op1)
				applyArrayOp2(t, d2, op2)

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.Equal(t, d1.Marshal(), d2.Marshal(), "Mismatch after both ops")

				// Undo
				assert.NoError(t, d1.Undo())
				assert.NoError(t, d2.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "Mismatch after both undos")
			})
		}
	}
}

// TestHistoryClearOnAttach pins that pre-attach changes are not reachable
// via undo once a document is attached (yorkie-js-sdk #1238).
func TestHistoryClearOnAttach(t *testing.T) {
	clients := activeClients(t, 1)
	cli := clients[0]
	defer deactivateAndCloseClients(t, clients)

	t.Run("history is cleared on attach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("c", 1)
			return nil
		}))
		assert.True(t, doc.CanUndo())
		assert.NoError(t, cli.Attach(ctx, doc))
		defer func() { assert.NoError(t, cli.Detach(ctx, doc)) }()
		assert.False(t, doc.CanUndo())
	})
}
