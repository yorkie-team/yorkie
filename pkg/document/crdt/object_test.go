/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

func TestObject(t *testing.T) {
	t.Run("marshal test", func(t *testing.T) {
		root := helper.TestRoot()
		ctx := helper.TextChangeContext(root)

		obj := crdt.NewObject(crdt.NewElementRHT(), ctx.IssueTimeTicket())

		primitive, err := crdt.NewPrimitive("v1", ctx.IssueTimeTicket())
		assert.NoError(t, err)
		obj.Set("k1", primitive)
		assert.Equal(t, `{"k1":"v1"}`, obj.Marshal())
		primitive, err = crdt.NewPrimitive("v2", ctx.IssueTimeTicket())
		assert.NoError(t, err)
		obj.Set("k2", primitive)
		assert.Equal(t, `{"k1":"v1","k2":"v2"}`, obj.Marshal())
		obj.Delete("k1", ctx.IssueTimeTicket())
		assert.Equal(t, `{"k2":"v2"}`, obj.Marshal())
	})
}
