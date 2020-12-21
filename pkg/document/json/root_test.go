/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package json_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/testhelper"
)

func registerTextElement(fromPos, toPos *json.RGATreeSplitNodePos, root *json.Root, text json.TextElement) {
	if fromPos.Compare(toPos) != 0 {
		root.RegisterRemovedNodeTextElement(text)
	}
}

func TestRoot(t *testing.T) {
	t.Run("garbage collect for text test", func(t *testing.T) {
		root := testhelper.TestRoot()
		ctx := testhelper.TextChangeContext(root)
		text := json.NewText(json.NewRGATreeSplit(json.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos := text.CreateRange(0, 0)
		text.Edit(fromPos, toPos, nil, "Hello World", ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, text)
		assert.Equal(t, `"Hello World"`, text.Marshal())
		assert.Equal(t, 0, root.GarbageLen())

		fromPos, toPos = text.CreateRange(6, 11)
		text.Edit(fromPos, toPos, nil, "Yorkie", ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, text)
		assert.Equal(t, `"Hello Yorkie"`, text.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		fromPos, toPos = text.CreateRange(0, 6)
		text.Edit(fromPos, toPos, nil, "", ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, text)
		assert.Equal(t, `"Yorkie"`, text.Marshal())
		assert.Equal(t, 2, root.GarbageLen())

		// It contains code marked tombstone.
		// After calling the garbage collector, the node will be removed.
		nodeLen := len(text.Nodes())
		assert.Equal(t, 3, nodeLen)

		assert.Equal(t, 2, root.GarbageCollect(time.MaxTicket))
		assert.Equal(t, 0, root.GarbageLen())
		nodeLen = len(text.Nodes())
		assert.Equal(t, 1, nodeLen)
	})

	t.Run("garbage collect for rich text test", func(t *testing.T) {
		root := testhelper.TestRoot()
		ctx := testhelper.TextChangeContext(root)
		richText := json.NewRichText(json.NewRGATreeSplit(json.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos := richText.CreateRange(0, 0)
		richText.Edit(fromPos, toPos, nil, "Hello World", nil, ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, richText)
		assert.Equal(t, `[{"attrs":{},"val":"Hello World"}]`, richText.Marshal())
		assert.Equal(t, 0, root.GarbageLen())

		fromPos, toPos = richText.CreateRange(6, 11)
		richText.Edit(fromPos, toPos, nil, "Yorkie", nil, ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, richText)
		assert.Equal(t, `[{"attrs":{},"val":"Hello "},{"attrs":{},"val":"Yorkie"}]`, richText.Marshal())
		assert.Equal(t, 1, root.GarbageLen())

		fromPos, toPos = richText.CreateRange(0, 6)
		richText.Edit(fromPos, toPos, nil, "", nil, ctx.IssueTimeTicket())
		registerTextElement(fromPos, toPos, root, richText)
		assert.Equal(t, `[{"attrs":{},"val":"Yorkie"}]`, richText.Marshal())
		assert.Equal(t, 2, root.GarbageLen())

		// It contains code marked tombstone.
		// After calling the garbage collector, the node will be removed.
		nodeLen := len(richText.Nodes())
		assert.Equal(t, 3, nodeLen)

		assert.Equal(t, 2, root.GarbageCollect(time.MaxTicket))
		assert.Equal(t, 0, root.GarbageLen())

		nodeLen = len(richText.Nodes())
		assert.Equal(t, 1, nodeLen)
	})
}
