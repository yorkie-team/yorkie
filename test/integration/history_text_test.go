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

// This file ports history_text_test.ts's "single client basic", "single
// client chained ops", "single client edge cases", "multi client basic",
// and "multi client edge cases" describe blocks. Its "reconcile cases"
// describe block (Case 1-7, plus the two `it.skip` correctness cases) is
// already ported faithfully by test/integration/history_text_reconcile_test.go
// -- TestHistoryTextReconcile covers Case 1-7, and
// TestReconcileOverlappingUndoDuplicatesContent covers both correctness
// cases live (not skipped -- see that file's doc comment for why the JS
// skip is stale). Nothing from that describe block is duplicated here.

// NOTE(hackerwins): applyTextOp1/applyTextOp2 and the few subtests that
// compute their own bounds derive indices from len(txt.String()), which is a
// byte count. Text indices are UTF-16 code units, so the two agree only for
// ASCII -- which every fixture in this file is. The first non-ASCII fixture
// added here must switch to a UTF-16 length, or it will silently address the
// wrong position rather than fail loudly.

// applyTextOp1 applies the given operation to the "t" text using the JS
// test's applyTextOp1 index pattern (history_text_test.ts).
func applyTextOp1(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		txt := root.GetText("t")
		length := len(txt.String())

		switch op {
		case "insert":
			txt.Edit(length, length, "X")
		case "delete":
			if length >= 3 {
				txt.Edit(1, 2, "")
			} else if length > 0 {
				txt.Edit(0, 1, "")
			}
		case "replace":
			if length >= 3 {
				txt.Edit(1, 3, "12")
			} else {
				end := 1
				if length < end {
					end = length
				}
				txt.Edit(0, end, "R")
			}
		case "style":
			if length == 0 {
				txt.Edit(0, 0, "A")
			}
			// Go's Style stores attribute values as strings only, unlike
			// JS's typed values (`{ bold: true }` is a JSON boolean) --
			// see the "should undo/redo style op" subtest below for the
			// matching Marshal() adjustment.
			txt.Style(0, len(txt.String()), map[string]string{"bold": "true"})
		}
		return nil
	}, op))
}

// applyTextOp2 applies the given operation to the "t" text using the JS
// test's applyTextOp2 index pattern (history_text_test.ts). JS's
// applyTextOp2 has no "style" case; ops2 callers never pass "style".
func applyTextOp2(t *testing.T, doc *document.Document, op string) {
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		txt := root.GetText("t")
		length := len(txt.String())

		switch op {
		case "insert":
			txt.Edit(0, 0, "Q")
		case "delete":
			if length > 0 {
				txt.Edit(length-1, length, "")
			}
		case "replace":
			if length > 0 {
				txt.Edit(0, 1, "Z")
			} else {
				txt.Edit(0, 0, "Z")
			}
		}
		return nil
	}, op))
}

// TestHistoryTextSingleClientBasic ports history_text_test.ts's "Text
// History - single client basic" describe block (:67-144): 6 runtime
// instances -- one per op in {insert, delete, replace}, plus 3 fixed
// cases.
func TestHistoryTextSingleClientBasic(t *testing.T) {
	ops := []string{"insert", "delete", "replace"}
	for _, op := range ops {
		op := op
		t.Run(fmt.Sprintf("should undo/redo %s", op), func(t *testing.T) {
			doc := document.New(helper.TestKey(t))
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("t").Edit(0, 0, "The fox jumped.")
				return nil
			}, "init"))

			before := doc.Root().GetText("t").String()
			applyTextOp1(t, doc, op)
			after := doc.Root().GetText("t").String()

			assert.NoError(t, doc.Undo())
			assert.Equal(t, before, doc.Root().GetText("t").String(), fmt.Sprintf("undo %s failed", op))

			assert.NoError(t, doc.Redo())
			assert.Equal(t, after, doc.Root().GetText("t").String(), fmt.Sprintf("redo %s failed", op))
		})
	}

	t.Run("should handle undo-redo round trip multiple times", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		initial := doc.Root().GetText("t").String()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(2, 2, "XY")
			return nil
		}, "insert"))
		modified := doc.Root().GetText("t").String()

		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Undo())
			assert.Equal(t, initial, doc.Root().GetText("t").String(), fmt.Sprintf("round %d undo failed", i))

			assert.NoError(t, doc.Redo())
			assert.Equal(t, modified, doc.Root().GetText("t").String(), fmt.Sprintf("round %d redo failed", i))
		}
	})

	t.Run("should clear redo stack when new edit is made after undo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(4, 4, "EF")
			return nil
		}, "append"))

		assert.NoError(t, doc.Undo())
		assert.True(t, doc.CanRedo())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "Z")
			return nil
		}, "new edit"))
		assert.False(t, doc.CanRedo())
	})

	t.Run("should undo/redo style op", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "The fox jumped.")
			return nil
		}, "init"))

		initialJSON := doc.Marshal()
		// JS's expected string is
		// `{"t":[{"attrs":{"bold":true},"val":"The fox jumped."}]}` --
		// a JSON boolean. Go's Style only stores string-valued
		// attributes (see applyTextOp1's "style" case), so the
		// faithful Go equivalent uses the string "true".
		styledJSON := `{"t":[{"attrs":{"bold":"true"},"val":"The fox jumped."}]}`

		applyTextOp1(t, doc, "style")
		assert.Equal(t, styledJSON, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, initialJSON, doc.Marshal())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, styledJSON, doc.Marshal())
	})
}

// TestHistoryTextSingleClientChainedOps ports history_text_test.ts's "Text
// History - single client chained ops" describe block (:146-184): 27
// runtime instances, the Cartesian product of {insert, delete, replace}^3.
func TestHistoryTextSingleClientChainedOps(t *testing.T) {
	ops := []string{"insert", "delete", "replace"}
	for _, op1 := range ops {
		for _, op2 := range ops {
			for _, op3 := range ops {
				op1, op2, op3 := op1, op2, op3
				caseName := fmt.Sprintf("%s-%s-%s", op1, op2, op3)

				t.Run(fmt.Sprintf("should undo chain correctly: %s", caseName), func(t *testing.T) {
					doc := document.New(helper.TestKey(t))
					assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
						root.SetNewText("t").Edit(0, 0, "ABCD")
						return nil
					}, "init"))

					snapshots := []string{doc.Root().GetText("t").String()}
					applyTextOp1(t, doc, op1)
					snapshots = append(snapshots, doc.Root().GetText("t").String())
					applyTextOp1(t, doc, op2)
					snapshots = append(snapshots, doc.Root().GetText("t").String())
					applyTextOp1(t, doc, op3)
					snapshots = append(snapshots, doc.Root().GetText("t").String())

					// Undo: S3 -> S2 -> S1 -> S0
					for i := 3; i >= 1; i-- {
						assert.NoError(t, doc.Undo())
						assert.Equal(t, snapshots[i-1], doc.Root().GetText("t").String(), fmt.Sprintf("undo to S%d", i-1))
					}

					// Redo: S0 -> S1 -> S2 -> S3
					for i := 0; i < 3; i++ {
						assert.NoError(t, doc.Redo())
						assert.Equal(t, snapshots[i+1], doc.Root().GetText("t").String(), fmt.Sprintf("redo to S%d", i+1))
					}
				})
			}
		}
	}
}

// TestHistoryTextSingleClientEdgeCases ports history_text_test.ts's "Text
// History - single client edge cases" describe block (:186-354): 9 runtime
// instances.
func TestHistoryTextSingleClientEdgeCases(t *testing.T) {
	t.Run("should handle edit at start position", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 2, "")
			return nil
		}, "delete at start"))
		assert.Equal(t, "CD", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "CD", doc.Root().GetText("t").String())
	})

	t.Run("should handle edit at end position", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(2, 4, "")
			return nil
		}, "delete at end"))
		assert.Equal(t, "AB", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "AB", doc.Root().GetText("t").String())
	})

	t.Run("should handle insert into empty text", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}, "init"))
		assert.Equal(t, "", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "Hello")
			return nil
		}, "insert"))
		assert.Equal(t, "Hello", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "Hello", doc.Root().GetText("t").String())
	})

	t.Run("should handle full deletion then undo", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 4, "")
			return nil
		}, "delete all"))
		assert.Equal(t, "", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABCD", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "", doc.Root().GetText("t").String())
	})

	t.Run("should handle full replacement", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "OLD")
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			length := len(root.GetText("t").String())
			root.GetText("t").Edit(0, length, "NEW")
			return nil
		}, "replace all"))
		assert.Equal(t, "NEW", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "OLD", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Redo())
		assert.Equal(t, "NEW", doc.Root().GetText("t").String())
	})

	t.Run("should handle single character operations", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABC")
			return nil
		}, "init"))

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(1, 1, "X")
			return nil
		}, "insert X"))
		assert.Equal(t, "AXBC", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABC", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(1, 2, "")
			return nil
		}, "delete B"))
		assert.Equal(t, "AC", doc.Root().GetText("t").String())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, "ABC", doc.Root().GetText("t").String())
	})

	t.Run("should handle empty undo stack", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.False(t, doc.CanUndo())
	})

	t.Run("should handle empty redo stack", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))

		assert.False(t, doc.CanRedo())
	})

	t.Run("should handle rapid consecutive edits", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}, "init"))

		states := []string{""}
		for i := 0; i < 10; i++ {
			i := i
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				length := len(root.GetText("t").String())
				root.GetText("t").Edit(length, length, fmt.Sprintf("%d", i))
				return nil
			}, fmt.Sprintf("insert %d", i)))
			states = append(states, doc.Root().GetText("t").String())
		}

		for i := 9; i >= 0; i-- {
			assert.NoError(t, doc.Undo())
			assert.Equal(t, states[i], doc.Root().GetText("t").String())
		}

		for i := 1; i <= 10; i++ {
			assert.NoError(t, doc.Redo())
			assert.Equal(t, states[i], doc.Root().GetText("t").String())
		}
	})
}

// TestHistoryTextMultiClientBasic ports history_text_test.ts's "Text
// History - multi client basic" describe block (:356-422): 18 runtime
// instances, {insert, delete, replace}^2 x {converge after undo, converge
// after redo}.
func TestHistoryTextMultiClientBasic(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	ops := []string{"insert", "delete", "replace"}
	for _, op1 := range ops {
		for _, op2 := range ops {
			op1, op2 := op1, op2

			t.Run(fmt.Sprintf("should converge after undo: %s-%s", op1, op2), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewText("t").Edit(0, 0, "The fox jumped.")
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				applyTextOp1(t, d1, op1)
				applyTextOp2(t, d2, op2)

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after ops")

				assert.NoError(t, d1.Undo())
				assert.NoError(t, d2.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after undo")
			})

			t.Run(fmt.Sprintf("should converge after redo: %s-%s", op1, op2), func(t *testing.T) {
				ctx := context.Background()

				d1 := document.New(helper.TestKey(t))
				assert.NoError(t, c1.Attach(ctx, d1))
				defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
				d2 := document.New(helper.TestKey(t))
				assert.NoError(t, c2.Attach(ctx, d2))
				defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					root.SetNewText("t").Edit(0, 0, "The fox jumped.")
					return nil
				}, "init"))
				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))

				applyTextOp1(t, d1, op1)
				applyTextOp2(t, d2, op2)

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Undo())
				assert.NoError(t, d2.Undo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))

				assert.NoError(t, d1.Redo())
				assert.NoError(t, d2.Redo())

				assert.NoError(t, c1.Sync(ctx))
				assert.NoError(t, c2.Sync(ctx))
				assert.NoError(t, c1.Sync(ctx))
				assert.Equal(t, d1.Marshal(), d2.Marshal(), "after redo")
			})
		}
	}
}

// TestHistoryTextMultiClientEdgeCases ports history_text_test.ts's "Text
// History - multi client edge cases" describe block (:777-908): 4 runtime
// instances.
func TestHistoryTextMultiClientEdgeCases(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("should converge with same position concurrent edits", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(2, 2, "X")
			return nil
		}, "d1 insert"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(2, 2, "Y")
			return nil
		}, "d2 insert"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("should converge with concurrent full deletion and insertion", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCD")
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 4, "")
			return nil
		}, "d1 delete all"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "XY")
			return nil
		}, "d2 insert"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("should converge when one client undos and other redos", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "ABCDEFGH")
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(2, 4, "XX")
			return nil
		}, "d1 edit"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(6, 8, "YY")
			return nil
		}, "d2 edit"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// d1: undo then redo, d2: just undo.
		assert.NoError(t, d1.Undo())
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.NoError(t, d1.Redo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("should converge with concurrent style operations", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "The fox jumped.")
			return nil
		}, "init"))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(0, 15, map[string]string{"bold": "true"})
			return nil
		}, "bold"))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Style(4, 15, map[string]string{"italic": "true"})
			return nil
		}, "italic"))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.NoError(t, d1.Undo())
		assert.NoError(t, d2.Undo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		assert.NoError(t, d1.Redo())
		assert.NoError(t, d2.Redo())

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})
}
