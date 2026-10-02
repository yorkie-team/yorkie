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
					at := r.Intn(textLen(text) + 1)
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
					if textLen(text) == 0 {
						return nil
					}
					from := r.Intn(textLen(text))
					text.Edit(from, min(textLen(text), from+1+r.Intn(3)), "")
					return nil
				}))
			case op < 7:
				require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					text := root.GetText("t")
					if textLen(text) == 0 {
						return nil
					}
					from := r.Intn(textLen(text))
					text.Style(from, min(textLen(text), from+1+r.Intn(3)), map[string]string{"b": "1"})
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

// textLen is the live length of text in UTF-16 code units, the unit text
// indices are measured in.
func textLen(text *json.Text) int {
	return len(utf16.Encode([]rune(text.String())))
}
