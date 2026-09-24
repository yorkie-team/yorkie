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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// "ab🇰🇷cd": the flag is two regional indicators (U+1F1F0 U+1F1F7), each a
// non-BMP code point, so the value is 6 runes but 8 UTF-16 code units.
const surrogateText = "ab\U0001F1F0\U0001F1F7cd"

// surrogateTextNode builds <r><p>ab🇰🇷cd</p></r> and returns its text node,
// attached to a parent so splits can insert the right piece.
func surrogateTextNode(t *testing.T) *crdt.TreeNode {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := crdt.NewTree(crdt.NewTreeNode(helper.PosT(ctx), "r", nil), helper.TimeT(ctx))

	_, _, err := tree.EditT(0, 0, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "p", nil),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)

	_, _, err = tree.EditT(1, 1, []*crdt.TreeNode{
		crdt.NewTreeNode(helper.PosT(ctx), "text", nil, surrogateText),
	}, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)

	node := tree.Root().Children()[0].Children()[0]
	require.True(t, node.IsText())
	return node
}

// TestSplitTextKeepsUTF16Length covers the unit a split records for the left
// piece. Every offset measured against a text node counts UTF-16 code units, so
// a length recorded in runes leaves the node shorter than the positions that
// anchor into it believe.
func TestSplitTextKeepsUTF16Length(t *testing.T) {
	node := surrogateTextNode(t)
	assert.Equal(t, 8, node.Len(), "2 + 4 + 2 UTF-16 code units")

	split, _, err := node.SplitText(6, 0) // immediately after the flag
	require.NoError(t, err)
	require.NotNil(t, split)

	assert.Equal(t, "ab\U0001F1F0\U0001F1F7", node.Value)
	assert.Equal(t, 6, node.Len(),
		"the left piece spans UTF-16 offsets [0,6); its rune count (4) is not its length")
	assert.Equal(t, "cd", split.Value)
	assert.Equal(t, 2, split.Len())
}

// TestSplitTextAtSameOffsetTwiceIsNoOp covers what a stored document hit: a
// TreeEdit whose from and to are the same position resolves that anchor twice,
// and the second resolution re-splits the left piece at its own end. That must
// return at the offset == n.Len() guard rather than fall through to the range
// check, or the document stops replaying for good.
func TestSplitTextAtSameOffsetTwiceIsNoOp(t *testing.T) {
	node := surrogateTextNode(t)

	_, _, err := node.SplitText(6, 0)
	require.NoError(t, err)

	split, _, err := node.SplitText(6, 0)
	assert.NoError(t, err, "re-splitting the left piece at its own end is a no-op")
	assert.Nil(t, split)
}
