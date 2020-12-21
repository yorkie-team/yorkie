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
	"github.com/yorkie-team/yorkie/testhelper"
)

func TestArray(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := testhelper.TestRoot()
		ctx := testhelper.TextChangeContext(root)

		a := json.NewArray(json.NewRGATreeList(), ctx.IssueTimeTicket())

		a.Add(json.NewPrimitive("1", ctx.IssueTimeTicket()))
		assert.Equal(t, `["1"]`, a.Marshal())
		a.Add(json.NewPrimitive("2", ctx.IssueTimeTicket()))
		assert.Equal(t, `["1","2"]`, a.Marshal())
		a.Add(json.NewPrimitive("3", ctx.IssueTimeTicket()))
		assert.Equal(t, `["1","2","3"]`, a.Marshal())
	})
}
