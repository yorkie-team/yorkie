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

package document_test

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestTextNormalizePosMatchesChainWalk pins Text.NormalizePos to its
// definition: the anchor is the head, and the offset is the live length of
// every node before the position's node plus the offset inside it. That is
// what the JS SDK computes by walking the physical prev chain; Go reads it from
// the index tree instead, so the two must agree on every chain shape local
// editing, undo/redo and GC can produce -- including positions on tombstones,
// which remote edits and reverse operations anchor on.
func TestTextNormalizePosMatchesChainWalk(t *testing.T) {
	const alphabet = "abcdefg😀가"

	for seed := int64(1); seed <= 30; seed++ {
		r := rand.New(rand.NewSource(seed)) //nolint:gosec // seeded so a failing seed reproduces
		doc := document.New(helper.TestKey(t))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}))
		// Undo must reach the edits, never the text itself.
		require.NoError(t, doc.ClearHistory())

		for step := range 150 {
			switch op := r.Intn(10); {
			case op < 4:
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					text := root.GetText("t")
					bounds := textBoundaries(text)
					at := bounds[r.Intn(len(bounds))]
					var sb strings.Builder
					for range 1 + r.Intn(4) {
						runes := []rune(alphabet)
						sb.WriteRune(runes[r.Intn(len(runes))])
					}
					text.Edit(at, at, sb.String())
					return nil
				}))
			case op < 6:
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					text := root.GetText("t")
					bounds := textBoundaries(text)
					if len(bounds) == 1 {
						return nil
					}
					from := r.Intn(len(bounds) - 1)
					to := min(len(bounds)-1, from+1+r.Intn(3))
					text.Edit(bounds[from], bounds[to], "")
					return nil
				}))
			case op < 7:
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					text := root.GetText("t")
					bounds := textBoundaries(text)
					if len(bounds) == 1 {
						return nil
					}
					from := r.Intn(len(bounds) - 1)
					to := min(len(bounds)-1, from+1+r.Intn(3))
					text.Style(bounds[from], bounds[to], map[string]string{"b": "1"})
					return nil
				}))
			case op < 8:
				require.NoError(t, doc.Undo())
			case op < 9:
				require.NoError(t, doc.Redo())
			default:
				doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
			}

			assertNormalizePosMatchesChainWalk(t, doc.RootObject().Get("t").(*crdt.Text), seed, step)
		}
	}
}

func assertNormalizePosMatchesChainWalk(t *testing.T, text *crdt.Text, seed int64, step int) {
	t.Helper()

	head := text.RGATreeSplit().InitialHead().ID()
	prefix := 0
	for _, node := range text.Nodes() {
		width := node.Value().Len()
		for offset := 0; offset <= width; offset++ {
			pos := crdt.NewRGATreeSplitNodePos(node.ID(), offset)
			normalized, err := text.NormalizePos(pos)
			require.NoError(t, err, "seed %d step %d", seed, step)
			// Compared before asserting: the messages print the whole chain,
			// and building them on every passing check makes the test
			// quadratic.
			if !normalized.ID().Equal(head) {
				require.Failf(t, "not anchored on the head",
					"seed %d step %d: anchored on %s", seed, step, normalized.ID().ToTestString())
			}
			if normalized.RelativeOffset() != prefix+offset {
				require.Equal(t, prefix+offset, normalized.RelativeOffset(),
					"seed %d step %d: %s in %s", seed, step, pos.ToTestString(), text.ToTestString())
			}
		}
		prefix += node.Len()
	}
}

// textBoundaries returns the character boundaries of text, from 0 to its live
// length, in UTF-16 code units, the unit text indices are measured in. The
// index between the two units of a surrogate pair is not among them: Text
// rejects it.
func textBoundaries(text *json.Text) []int {
	bounds := []int{0}
	for _, r := range text.String() {
		bounds = append(bounds, bounds[len(bounds)-1]+utf16.RuneLen(r))
	}
	return bounds
}
