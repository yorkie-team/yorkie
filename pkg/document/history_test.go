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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestHistoryStack(t *testing.T) {
	t.Run("empty stack undo is a no-op test", func(t *testing.T) {
		doc := document.New("d1")
		assert.False(t, doc.CanUndo())
		assert.False(t, doc.CanRedo())
		assert.NoError(t, doc.Undo())
		assert.NoError(t, doc.Redo())
	})

	t.Run("undo inside an updater is refused test", func(t *testing.T) {
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			return doc.Undo()
		})
		assert.ErrorIs(t, err, document.ErrRefusedDuringUpdate)
	})

	t.Run("clear history inside an updater is refused test", func(t *testing.T) {
		// ClearHistory takes d.mu, which the updater already holds. Without
		// the same guard Undo and Redo use, this call would block forever on
		// its own goroutine's lock.
		doc := document.New("d1")
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
			return doc.ClearHistory()
		})
		assert.ErrorIs(t, err, document.ErrRefusedDuringUpdate)
	})

	t.Run("stack depth is capped test", func(t *testing.T) {
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("c", int64(0))
			return nil
		}))
		for i := 0; i < document.MaxUndoRedoStackDepth+10; i++ {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter("c").Increase(1)
				return nil
			}))
		}
		assert.Equal(t, document.MaxUndoRedoStackDepth, doc.UndoStackLenForTest())
	})

	t.Run("reconcile createdAt after array set test", func(t *testing.T) {
		// A Set replaces the element, giving it a new createdAt. Reverse
		// operations already on the stack still point at the old one and must
		// be rewritten, or a later undo targets a dead element.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("list").AddInteger(1, 2, 3)
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("list").SetInteger(0, 9)
			return nil
		}))
		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())
	})

	t.Run("array set undo redo survives gc test", func(t *testing.T) {
		// ArraySet's reverse restores the replaced value under a freshly
		// reissued createdAt (executeUndoRedo's ArraySet branch). This pins
		// that a full undo/redo cycle leaves the document intact and that
		// GC's view of the document -- whatever it tracks for ArraySet --
		// does not change out from under us.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("list").AddInteger(1, 2, 3)
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("list").SetInteger(0, 9)
			return nil
		}))
		assert.Equal(t, `{"list":[9,2,3]}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"list":[9,2,3]}`, doc.Marshal())
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, 0, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, `{"list":[9,2,3]}`, doc.Marshal())
	})

	t.Run("array add undo redo survives gc test", func(t *testing.T) {
		// Add's reverse is a Remove; undoing it deletes the added element.
		// Redo then replays Remove's own (pre-existing) Add reverse, which
		// restores the element under a reissued createdAt -- the same
		// collision hazard Task 5 fixed for a plain Remove. This exercises
		// the new Add.Execute reverse feeding back into that machinery.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("list").AddInteger(1, 2, 3)
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("list").AddInteger(4)
			return nil
		}))
		assert.Equal(t, `{"list":[1,2,3,4]}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"list":[1,2,3,4]}`, doc.Marshal())

		// The restored element must survive GC: redoing the Add must not
		// leave the reissued copy registered under the tombstoned element's
		// old identity, or a GC pass purges the live element instead of the
		// tombstone, silently reverting the redo.
		assert.Equal(t, 1, doc.GarbageLen())
		assert.Equal(t, 1, doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID())))
		assert.Equal(t, 0, doc.GarbageLen())
		assert.Equal(t, `{"list":[1,2,3,4]}`, doc.Marshal())
	})

	t.Run("array move undo redo test", func(t *testing.T) {
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("list").AddInteger(1, 2, 3)
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("list").MoveAfterByIndex(0, 2)
			return nil
		}))
		assert.Equal(t, `{"list":[1,3,2]}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"list":[1,3,2]}`, doc.Marshal())
	})

	t.Run("reconcile createdAt after array move test", func(t *testing.T) {
		// A Move's reverse stores both a createdAt and a prevCreatedAt. Both
		// must be rewritten by ReconcileCreatedAt when the identity they
		// point at is replaced -- here, by an ArraySet on the element the
		// Move reverse anchors on.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("list").AddInteger(1, 2, 3)
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			// Move "3" to the front: [3,1,2]. The reverse Move's
			// prevCreatedAt anchors on "2", the element that preceded "3".
			list := root.GetArray("list")
			list.MoveFront(list.Get(2).CreatedAt())
			return nil
		}))
		assert.Equal(t, `{"list":[3,1,2]}`, doc.Marshal())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			// Replace "2": its identity, which the stacked Move reverse's
			// prevCreatedAt still anchors on, must be reconciled.
			root.GetArray("list").SetInteger(2, 9)
			return nil
		}))
		assert.Equal(t, `{"list":[3,1,9]}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[3,1,2]}`, doc.Marshal())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, `{"list":[1,2,3]}`, doc.Marshal())
	})

	t.Run("presence set with history undo redo test", func(t *testing.T) {
		// NOTE(hackerwins): MyPresence requires StatusAttached, which a bare
		// document.New never reaches. PresenceForTest reads the same map
		// without that guard, matching how the other tests in this file
		// exercise Undo/Redo without a client.
		doc := document.New("d1")
		myPresence := func() presence.Data {
			return doc.PresenceForTest(doc.ActorID().String())
		}

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "red")
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "blue", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "blue"}, myPresence())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": "red"}, myPresence())

		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, presence.Data{"color": "blue"}, myPresence())
	})

	t.Run("presence undo of a newly introduced key restores the zero value test", func(t *testing.T) {
		// change.Context.ReversePresence rebuilds a key's undo value from
		// previousPresence[key], a snapshot taken when the context was
		// created. For a key that did not exist in that snapshot -- i.e.
		// the tracked Set introduced it, rather than changing it -- Go's
		// map indexing yields the zero value, the empty string, so undo
		// restores "" rather than removing the key.
		//
		// JS's equivalent (context.ts's getReversePresence) assigns
		// `undefined` for the same case, and yorkie-js-sdk's deepcopy
		// (JSON.parse(JSON.stringify(...))) drops object keys whose value
		// is undefined before the change reaches the wire -- so JS
		// removes the key on undo instead of sending "".
		//
		// This is a confirmed, deliberate divergence between the two
		// SDKs, not a bug to fix here: see
		// docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md
		// ("Related: undoing a newly introduced presence key"). This test
		// pins Go's current behavior so a future change to it is a
		// conscious decision, not an accident.
		doc := document.New("d1")
		myPresence := func() presence.Data {
			return doc.PresenceForTest(doc.ActorID().String())
		}

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "blue", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "blue"}, myPresence())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": ""}, myPresence())
	})

	t.Run("disabled presence set with history leaves no undo entry test", func(t *testing.T) {
		// A document that opted out of presence must not resurrect presence
		// via undo: Update drops the presence change entirely, so no
		// reverse-presence entry should reach the undo stack even when the
		// key was marked WithHistory.
		doc := document.New("d1", document.WithDisablePresence())
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("color", "red", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, 0, doc.UndoStackLenForTest())
		assert.False(t, doc.CanUndo())
	})
}
