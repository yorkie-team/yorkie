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
}
