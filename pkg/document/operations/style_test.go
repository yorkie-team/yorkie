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

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// newStyleTestRoot builds a root holding a Text under key "t" with content
// "AB" carrying the attribute bold="true", so a test can style over it.
func newStyleTestRoot(t *testing.T, actor *time.ActorID) (*crdt.Root, *crdt.Text) {
	t.Helper()

	text := crdt.NewText(crdt.NewRGATreeSplit(crdt.InitialTextNode()), time.InitialTicket)
	fromPos, toPos, err := text.CreateRange(0, 0)
	assert.NoError(t, err)
	_, _, _, _, _, err = text.Edit(
		fromPos, toPos, "AB", map[string]string{"bold": "true"},
		time.NewTicket(1, 0, *actor), nil,
	)
	assert.NoError(t, err)

	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	obj.Set("t", text)
	root := crdt.NewRoot(obj)

	return root, text
}

func TestStyle(t *testing.T) {
	actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

	t.Run("reverse of a remove-only forward op restores the removed value", func(t *testing.T) {
		root, text := newStyleTestRoot(t, &actor)

		fromPos, toPos, err := text.CreateRange(0, 2)
		assert.NoError(t, err)

		removeOp := operations.NewStyleRemove(
			text.CreatedAt(), fromPos, toPos, []string{"bold"}, time.NewTicket(2, 0, actor),
		)
		reverse, err := removeOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		assert.NoError(t, err)
		assert.False(t, text.Nodes()[0].Value().Attrs().Has("bold"),
			"forward RemoveStyle should have removed the attribute")

		reverseStyle, ok := reverse.(*operations.Style)
		assert.True(t, ok, "reverse of a remove-only op should be a set-attributes Style")
		assert.Equal(t, map[string]string{"bold": "true"}, reverseStyle.Attributes())
		assert.Empty(t, reverseStyle.AttributesToRemove())

		reverseStyle.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = reverseStyle.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.NoError(t, err)
		assert.Equal(t, "true", text.Nodes()[0].Value().Attrs().Get("bold"),
			"executing the reverse should restore the removed value")
	})

	t.Run("reverse of a mixed set unions restore and removal", func(t *testing.T) {
		// bold already exists ("true"); italic does not. Styling both in one
		// call must produce a single reverse that both restores bold and
		// removes italic -- proving the two branches of Execute are not
		// mutually exclusive.
		root, text := newStyleTestRoot(t, &actor)

		fromPos, toPos, err := text.CreateRange(0, 2)
		assert.NoError(t, err)

		setOp := operations.NewStyle(
			text.CreatedAt(), fromPos, toPos,
			map[string]string{"bold": "false", "italic": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverse, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		assert.NoError(t, err)

		attrs := text.Nodes()[0].Value().Attrs()
		assert.Equal(t, "false", attrs.Get("bold"))
		assert.Equal(t, "true", attrs.Get("italic"))

		reverseStyle, ok := reverse.(*operations.Style)
		assert.True(t, ok, "reverse of a mixed set should be a Style carrying both branches")
		assert.Equal(t, map[string]string{"bold": "true"}, reverseStyle.Attributes())
		assert.Equal(t, []string{"italic"}, reverseStyle.AttributesToRemove())

		reverseStyle.SetExecutedAt(time.NewTicket(3, 0, actor))
		_, err = reverseStyle.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
		assert.NoError(t, err)

		attrs = text.Nodes()[0].Value().Attrs()
		assert.Equal(t, "true", attrs.Get("bold"), "bold should be restored to its prior value")
		assert.False(t, attrs.Has("italic"), "italic should be removed, not set to empty")
	})

	t.Run("styling with an unchanged value still returns a restoring reverse", func(t *testing.T) {
		root, text := newStyleTestRoot(t, &actor)

		fromPos, toPos, err := text.CreateRange(0, 2)
		assert.NoError(t, err)

		// Styling with the same value that is already present changes
		// nothing observable, but Text.Style still reports the prior value
		// (Existed: true) for the requested key, so the reverse still
		// restores it -- it is not nil merely because the value repeats.
		setOp := operations.NewStyle(
			text.CreatedAt(), fromPos, toPos,
			map[string]string{"bold": "true"},
			time.NewTicket(2, 0, actor),
		)
		reverse, err := setOp.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
		assert.NoError(t, err)
		assert.NotNil(t, reverse)
	})
}
