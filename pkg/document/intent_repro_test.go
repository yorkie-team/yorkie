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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// intentInsert inserts s at index at, as one change.
func intentInsert(t *testing.T, d *document.Document, at int, s string) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(at, at, s)
		return nil
	}))
}

func intentText(d *document.Document) string {
	return d.Root().GetText("t").String()
}

// intentShared gives both replicas the same Text holding base.
func intentShared(t *testing.T, base string) (*document.Document, *document.Document) {
	t.Helper()
	d1, d2, _, _ := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, base)
		return nil
	}))
	crossSync(t, d1, d2)
	return d1, d2
}

// intentForward inserts each character after the previous one.
func intentForward(t *testing.T, d *document.Document, at int, chars []string) {
	for i, c := range chars {
		intentInsert(t, d, at+i, c)
	}
}

// intentBackward keeps the caret fixed: each character is inserted before the
// previous one, so the run is typed last character first.
func intentBackward(t *testing.T, d *document.Document, at int, chars []string) {
	for i := len(chars) - 1; i >= 0; i-- {
		intentInsert(t, d, at, chars[i])
	}
}

// TestIntentRepro shows the merged result when two replicas type a word at
// the same position concurrently. Both replicas converge in every case; the
// backward cases interleave the two words.
func TestIntentRepro(t *testing.T) {
	type typer func(*testing.T, *document.Document, int, []string)
	strategies := map[string]typer{"forward": intentForward, "backward": intentBackward}

	for _, tc := range []struct{ a, b string }{
		{"forward", "forward"},
		{"forward", "backward"},
		{"backward", "forward"},
		{"backward", "backward"},
	} {
		name := tc.a + "_vs_" + tc.b
		t.Run(name, func(t *testing.T) {
			d1, d2 := intentShared(t, "XY")
			strategies[tc.a](t, d1, 1, []string{"가", "나", "다"})
			strategies[tc.b](t, d2, 1, []string{"a", "b", "c"})
			crossSync(t, d1, d2)

			require.Equal(t, intentText(d1), intentText(d2))
			t.Logf("RESULT %s: %s", name, intentText(d1))
		})
	}

	t.Run("single_op_words", func(t *testing.T) {
		d1, d2 := intentShared(t, "XY")
		intentInsert(t, d1, 1, "가나다")
		intentInsert(t, d2, 1, "abc")
		crossSync(t, d1, d2)

		require.Equal(t, intentText(d1), intentText(d2))
		// Each word is one insertion at the same caret, so both carry the
		// same left origin (after X) and the same right origin (Y): nothing
		// in the operations says which word was meant to come first, and the
		// order falls to the actor tie-break. What the two-origin rule does
		// guarantee is that the words do not interleave.
		require.Contains(t, []string{"Xabc가나다Y", "X가나다abcY"}, intentText(d1))
		t.Logf("RESULT single_op_words: %s", intentText(d1))
	})
}

// TestArrayMoveIntentRepro: A inserts "b" right after "a" while B moves "a"
// to the end. Both converge, but "b" no longer follows "a". This is the
// non-commutative Move family tracked by #1382, recorded here for contrast.
func TestArrayMoveIntentRepro(t *testing.T) {
	d1, d2, _, _ := newReplicas(t)
	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewArray("a").AddString("a", "x", "y")
		return nil
	}))
	crossSync(t, d1, d2)

	require.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetArray("a").InsertStringAfter(0, "b")
		return nil
	}))
	require.NoError(t, d2.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetArray("a").MoveAfterByIndex(2, 0)
		return nil
	}))
	crossSync(t, d1, d2)

	require.Equal(t, d1.Marshal(), d2.Marshal())
	require.Equal(t, `{"a":["b","x","y","a"]}`, d1.Marshal())
	t.Logf("RESULT array_move: %s", d1.Marshal())
}

// TestHangulIMERepro types 가나다 the way a Hangul IME does — each syllable
// first appears as a jamo, which is then replaced by the composed syllable —
// against plain forward typing on the other replica, for both actor orders.
func TestHangulIMERepro(t *testing.T) {
	ime := func(t *testing.T, d *document.Document, at int) {
		jamo := []string{"ㄱ", "ㄴ", "ㄷ"}
		syll := []string{"가", "나", "다"}
		for i := range syll {
			intentInsert(t, d, at+i, jamo[i])
			require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
				root.GetText("t").Edit(at+i, at+i+1, syll[i])
				return nil
			}))
		}
	}

	for _, imeFirst := range []bool{true, false} {
		name := "ime_on_actor_02"
		if imeFirst {
			name = "ime_on_actor_01"
		}
		t.Run(name, func(t *testing.T) {
			d1, d2 := intentShared(t, "XY")
			imeDoc, otherDoc := d2, d1
			if imeFirst {
				imeDoc, otherDoc = d1, d2
			}
			ime(t, imeDoc, 1)
			intentForward(t, otherDoc, 1, []string{"a", "b", "c"})
			crossSync(t, d1, d2)

			require.Equal(t, intentText(d1), intentText(d2))
			require.Contains(t, intentText(d1), "가나다")
			require.Contains(t, intentText(d1), "abc")
			t.Logf("RESULT hangul_ime %s: %s", name, intentText(d1))
		})
	}
}
