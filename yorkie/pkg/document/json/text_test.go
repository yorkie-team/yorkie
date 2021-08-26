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
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestText(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)
		text := json.NewText(json.NewRGATreeSplit(json.InitialTextNode()), ctx.IssueTimeTicket())

		fromPos, toPos := text.CreateRange(0, 0)
		text.Edit(fromPos, toPos, nil, "Hello World", ctx.IssueTimeTicket())
		assert.Equal(t, `"Hello World"`, text.Marshal())

		fromPos, toPos = text.CreateRange(6, 11)
		text.Edit(fromPos, toPos, nil, "Yorkie", ctx.IssueTimeTicket())
		assert.Equal(t, `"Hello Yorkie"`, text.Marshal())
	})

	t.Run("UTF-16 code units test", func(t *testing.T) {
		tests := []struct {
			length int
			value  string
		}{
			{4, "abcd"},
			{2, "한글"},
			{8, "अनुच्छेद"},
			{12, "🌷🎁💩😜👍🏳"},
			{10, "Ĺo͂řȩm̅"},
		}
		for _, test := range tests {
			val := json.NewTextValue(test.value)
			assert.Equal(t, test.length, val.Len())
			assert.Equal(t, test.length-2, val.Split(2).Len())

			richVal := json.NewRichTextValue(json.NewRHT(), test.value)
			assert.Equal(t, test.length, richVal.Len())
			assert.Equal(t, test.length-2, richVal.Split(2).Len())
		}
	})
}
