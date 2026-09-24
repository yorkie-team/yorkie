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

package packs

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func newChangeWith(ops ...operations.Operation) *change.Change {
	return change.New(newID(), "", ops, nil)
}

func newDeleteEditOp() operations.Operation {
	return operations.NewEdit(time.InitialTicket, nil, nil, "", nil, time.InitialTicket)
}

func newInsertEditOp() operations.Operation {
	return operations.NewEdit(time.InitialTicket, nil, nil, "hello", nil, time.InitialTicket)
}

func newDeleteTreeEditOp() operations.Operation {
	return operations.NewTreeEdit(time.InitialTicket, nil, nil, nil, 0, time.InitialTicket)
}

func TestMayGrowDocument(t *testing.T) {
	t.Run("a presence-only change cannot touch the root", func(t *testing.T) {
		assert.False(t, mayGrowDocument(change.New(newID(), "", nil, newPresenceChange())))
	})

	t.Run("removals and empty edits do not grow", func(t *testing.T) {
		assert.False(t, mayGrowDocument(newChangeWith(newRemoveOp())))
		assert.False(t, mayGrowDocument(newChangeWith(newDeleteEditOp())))
		assert.False(t, mayGrowDocument(newChangeWith(newDeleteTreeEditOp())))
		assert.False(t, mayGrowDocument(newChangeWith(
			newRemoveOp(), newDeleteEditOp(), newDeleteTreeEditOp(),
		)))
	})

	t.Run("an edit carrying content or attributes grows", func(t *testing.T) {
		assert.True(t, mayGrowDocument(newChangeWith(newInsertEditOp())))
		assert.True(t, mayGrowDocument(newChangeWith(
			operations.NewEdit(time.InitialTicket, nil, nil, "",
				map[string]string{"b": "true"}, time.InitialTicket),
		)))
	})

	t.Run("a tree edit with contents or a split level grows", func(t *testing.T) {
		assert.True(t, mayGrowDocument(newChangeWith(
			operations.NewTreeEdit(time.InitialTicket, nil, nil, nil, 1, time.InitialTicket),
		)))
	})

	t.Run("unclassified operations are treated as growth", func(t *testing.T) {
		assert.True(t, mayGrowDocument(newChangeWith(
			operations.NewMove(time.InitialTicket, time.InitialTicket,
				time.InitialTicket, time.InitialTicket),
		)))
		assert.True(t, mayGrowDocument(newChangeWith(
			operations.NewIncrease(time.InitialTicket, nil, time.InitialTicket),
		)))
	})

	t.Run("one growing operation taints the whole change", func(t *testing.T) {
		assert.True(t, mayGrowDocument(newChangeWith(newRemoveOp(), newInsertEditOp())))
	})
}

func TestCheckDocSize(t *testing.T) {
	growing := []*change.Change{newChangeWith(newInsertEditOp())}
	shrinking := []*change.Change{newChangeWith(newRemoveOp())}

	t.Run("an unset quota admits everything", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 0}
		info := &database.DocInfo{DocSize: 1 << 30}
		assert.NoError(t, checkDocSize(project, info, growing))
	})

	t.Run("an unmeasured document is admitted", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 100}
		info := &database.DocInfo{DocSize: 0}
		assert.NoError(t, checkDocSize(project, info, growing))
	})

	t.Run("a document at or under the quota is admitted", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 100}
		assert.NoError(t, checkDocSize(project, &database.DocInfo{DocSize: 99}, growing))
		assert.NoError(t, checkDocSize(project, &database.DocInfo{DocSize: 100}, growing))
	})

	t.Run("an over-quota document refuses growth", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 100}
		info := &database.DocInfo{DocSize: 101}

		err := checkDocSize(project, info, growing)
		assert.ErrorIs(t, err, ErrDocumentSizeExceedsLimit)
	})

	t.Run("an over-quota document still admits deletions", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 100}
		info := &database.DocInfo{DocSize: 1 << 30}

		assert.NoError(t, checkDocSize(project, info, shrinking))
		assert.NoError(t, checkDocSize(project, info, nil))
	})

	t.Run("a pack is refused whole when any change grows", func(t *testing.T) {
		project := &types.Project{MaxSizePerDocument: 100}
		info := &database.DocInfo{DocSize: 101}

		err := checkDocSize(project, info, append(append([]*change.Change{},
			shrinking...), growing...))
		assert.ErrorIs(t, err, ErrDocumentSizeExceedsLimit)
	})
}
