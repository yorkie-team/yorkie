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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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

	t.Run("deep copy preserves a member restored under an older createdAt", func(t *testing.T) {
		// Regression test: DeepCopy used to rebuild the RHT by replaying
		// Set(key, copiedNode), which uses the copied node's own createdAt
		// as the LWW tie-break ticket -- re-running the LWW race instead of
		// copying the structure as-is. A member restored by undo/redo keeps
		// its original (older) createdAt but has a newer movedAt; replaying
		// Set with executedAt=createdAt loses that race against the value
		// it had just beaten, and the replayed loser gets marked removed,
		// so DeepCopy silently drops a live key.
		actorA := time.ActorID{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
		t1 := time.NewTicket(1, 0, actorA)
		t5 := time.NewTicket(5, 0, actorA)
		t9 := time.NewTicket(9, 0, actorA)

		rht := crdt.NewElementRHT()
		v1, err := crdt.NewPrimitive("v1", t1)
		assert.NoError(t, err)
		rht.Set("key", v1)
		v2, err := crdt.NewPrimitive("v2", t5)
		assert.NoError(t, err)
		rht.Set("key", v2)

		// Restore v1 the way undo/redo does: original createdAt (t1), fresh
		// executedAt (t9) distinct from that createdAt.
		v1Restored, err := crdt.NewPrimitive("v1", t1)
		assert.NoError(t, err)
		rht.SetWithExecutedAt("key", v1Restored, t9)

		obj := crdt.NewObject(rht, time.InitialTicket)
		assert.Equal(t, `{"key":"v1"}`, obj.Marshal())

		copied, err := obj.DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, `{"key":"v1"}`, copied.Marshal())
	})

	t.Run("deep copy does not resurrect a deleted Text member", func(t *testing.T) {
		// Regression test: Text.DeepCopy (like Tree.DeepCopy) did not
		// propagate removedAt, so a deleted Text member came back as live
		// in the clone -- not just a size mismatch (caught indirectly by
		// TestDocumentSize), but a resurrection bug: the user's Update
		// closure reads through a DeepCopy'd cloneRoot, and the
		// cached-snapshot path copies through DeepCopy too.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, "hello")
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("t")
			return nil
		}))
		assert.Equal(t, `{}`, doc.Marshal())

		copied, err := doc.RootObject().DeepCopy()
		assert.NoError(t, err)
		assert.Equal(t, `{}`, copied.Marshal())
	})
}
