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
	"errors"
	"fmt"
	"sync"
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

	t.Run("a no-op edit keeps the redo stack test", func(t *testing.T) {
		// Both gates that JS puts on `opInfos.length` -- "did this change
		// produce anything observable" -- must ask the same question in Go,
		// not "did any operation run". An edit that neither inserts nor
		// removes runs and produces nothing, so JS leaves the redo stack
		// alone (document.ts:768).
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "hi")
			return nil
		}))
		assert.NoError(t, doc.Undo())
		assert.True(t, doc.CanRedo())

		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "")
			return nil
		}))
		assert.True(t, doc.CanRedo())
	})

	t.Run("undoing a no-op edit queues no change test", func(t *testing.T) {
		// The reverse of a no-op edit is itself a no-op, so undoing it
		// changes nothing any peer could observe. JS returns early before
		// localChanges.push (document.ts:2145); Go must not spend a clientSeq
		// and a change-log row on it either.
		doc := document.New("d1")
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("t")
			return nil
		}))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("t").Edit(0, 0, "")
			return nil
		}))

		before := len(doc.CreateChangePack().Changes)
		assert.True(t, doc.CanUndo())
		assert.NoError(t, doc.Undo())
		assert.Equal(t, before, len(doc.CreateChangePack().Changes))
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

	t.Run("concurrent updates keep the updating flag owned test", func(t *testing.T) {
		// The updating flag must be true only while the owning goroutine
		// holds d.mu. Raised before the lock instead, a second Update could
		// lower it on its way out while the first is still inside its
		// updater, and the first's re-entrant Undo would then miss the guard
		// and block on the mutex it already holds.
		doc := document.New("d1")
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				errs[idx] = doc.Update(func(root *json.Object, p *presence.Presence) error {
					// Every updater re-enters; each must be refused rather
					// than deadlock, however the goroutines interleave.
					if err := doc.Undo(); !errors.Is(err, document.ErrRefusedDuringUpdate) {
						return fmt.Errorf("undo returned %v", err)
					}
					if err := doc.ClearHistory(); !errors.Is(err, document.ErrRefusedDuringUpdate) {
						return fmt.Errorf("clear history returned %v", err)
					}
					assert.False(t, doc.CanUndo())
					assert.False(t, doc.CanRedo())
					root.SetNewCounter(fmt.Sprintf("c%d", idx), int64(0))
					return nil
				})
			}(i)
		}
		wg.Wait()
		for _, err := range errs {
			assert.NoError(t, err)
		}
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

	t.Run("presence undo of a newly introduced key removes it test", func(t *testing.T) {
		// change.Context.ReversePresence rebuilds a key's undo value from
		// previousPresence, a snapshot taken when the context was created.
		// A key missing from that snapshot -- the tracked Set introduced it
		// rather than changing it -- has no value to restore, so undo has
		// to remove it. Indexing the snapshot map instead yields Go's zero
		// value and would leave the key present as "".
		//
		// JS reads `undefined` for the same key (context.ts's
		// getReversePresence) and its deepcopy (JSON.parse(JSON.stringify
		// (...))) drops keys whose value is undefined before the change
		// reaches the wire, so JS removes the key too. This test previously
		// pinned Go's "" as a deliberate divergence; it was a defect, and
		// now pins the corrected, JS-matching behavior.
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
		assert.Equal(t, presence.Data{}, myPresence())

		// The redo has a value to restore, so it comes back unchanged.
		assert.True(t, doc.CanRedo())
		assert.NoError(t, doc.Redo())
		assert.Equal(t, presence.Data{"color": "blue"}, myPresence())
	})

	t.Run("presence undo removes only the newly introduced keys test", func(t *testing.T) {
		// A single change can both introduce a key and change an existing
		// one. Undo must remove the first and restore the second.
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
			p.Set("shape", "circle", presence.WithHistory())
			return nil
		}))
		assert.Equal(t, presence.Data{"color": "blue", "shape": "circle"}, myPresence())

		assert.NoError(t, doc.Undo())
		assert.Equal(t, presence.Data{"color": "red"}, myPresence())
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
