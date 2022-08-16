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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRichText(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		text := crdt.NewInitialRichText(crdt.NewRGATreeSplit(crdt.InitialRichTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos := text.CreateRange(0, 0)
		text.Edit(fromPos, toPos, nil, "Hello World", nil, ctx.IssueTimeTicket())
		assert.Equal(t, `[{"attrs":{},"val":"Hello World"}]`, text.Marshal())

		fromPos, toPos = text.CreateRange(6, 11)
		text.Edit(fromPos, toPos, nil, "Yorkie", nil, ctx.IssueTimeTicket())
		assert.Equal(t, `[{"attrs":{},"val":"Hello "},{"attrs":{},"val":"Yorkie"}]`, text.Marshal())

		fromPos, toPos = text.CreateRange(0, 1)
		text.SetStyle(fromPos, toPos, map[string]string{"b": "1"}, ctx.IssueTimeTicket())
		assert.Equal(
			t,
			`[{"attrs":{"b":"1"},"val":"H"},{"attrs":{},"val":"ello "},{"attrs":{},"val":"Yorkie"}]`,
			text.Marshal(),
		)
	})
}
