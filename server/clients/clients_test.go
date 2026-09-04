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

package clients_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/memory"
	yorkiesync "github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/clients"
)

func newTestBackend(t *testing.T) *backend.Backend {
	db, err := memory.New()
	assert.NoError(t, err)

	return &backend.Backend{
		DB:      db,
		Lockers: yorkiesync.New(),
	}
}

// TestAttachDocumentResumeCheckpoint exercises the conditional checkpoint reset
// (Q3) through clients.AttachDocument: Case B seeding from the presented
// checkpoint, the serverSeq safety clamp, and a fresh attach starting at 0.
func TestAttachDocumentResumeCheckpoint(t *testing.T) {
	ctx := context.Background()
	be := newTestBackend(t)

	project, err := be.DB.CreateProjectInfo(ctx, t.Name(), types.ID("000000000000000000000000"))
	assert.NoError(t, err)

	activate := func(name string) (*database.ClientInfo, *database.DocInfo) {
		clientInfo, err := be.DB.ActivateClient(ctx, project.ID, name, nil)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindOrCreateDocInfo(
			ctx, clientInfo.RefKey(), key.Key(fmt.Sprintf("tests$%s", name)), false,
		)
		assert.NoError(t, err)
		return clientInfo, docInfo
	}

	t.Run("Case B seeds from presented checkpoint", func(t *testing.T) {
		clientInfo, docInfo := activate(t.Name())
		docInfo.ServerSeq = 5

		// A reload presents its persisted checkpoint. A non-zero serverSeq is the
		// resume signal; it stays within the doc's actual (5), so no clamp.
		presented := change.NewCheckpoint(5, 4)
		got, err := clients.AttachDocument(ctx, be, clientInfo, docInfo, false, presented)
		assert.NoError(t, err)

		cp := got.Checkpoint(docInfo.ID)
		assert.Equal(t, uint32(4), cp.ClientSeq)
		assert.Equal(t, int64(5), cp.ServerSeq)
	})

	t.Run("fresh attach with local edits seeds zero", func(t *testing.T) {
		clientInfo, docInfo := activate(t.Name())

		// A non-zero clientSeq with serverSeq 0 is a fresh attach carrying local
		// edits, not a resume; it must seed 0/0 so the pending changes push.
		got, err := clients.AttachDocument(ctx, be, clientInfo, docInfo, false, change.NewCheckpoint(0, 3))
		assert.NoError(t, err)

		cp := got.Checkpoint(docInfo.ID)
		assert.Equal(t, int64(0), cp.ServerSeq)
		assert.Equal(t, uint32(0), cp.ClientSeq)
	})

	t.Run("serverSeq clamp caps an over-claimed presented serverSeq", func(t *testing.T) {
		clientInfo, docInfo := activate(t.Name())
		docInfo.ServerSeq = 2

		// The client over-claims serverSeq=99; the clamp caps it to the doc's
		// actual (2) so it re-pulls what it has not really seen. clientSeq is
		// untouched.
		presented := change.NewCheckpoint(99, 7)
		got, err := clients.AttachDocument(ctx, be, clientInfo, docInfo, false, presented)
		assert.NoError(t, err)

		cp := got.Checkpoint(docInfo.ID)
		assert.Equal(t, int64(2), cp.ServerSeq)
		assert.Equal(t, uint32(7), cp.ClientSeq)
	})

	t.Run("fresh attach seeds zero", func(t *testing.T) {
		clientInfo, docInfo := activate(t.Name())

		got, err := clients.AttachDocument(ctx, be, clientInfo, docInfo, false, change.InitialCheckpoint)
		assert.NoError(t, err)

		cp := got.Checkpoint(docInfo.ID)
		assert.Equal(t, int64(0), cp.ServerSeq)
		assert.Equal(t, uint32(0), cp.ClientSeq)
	})
}
