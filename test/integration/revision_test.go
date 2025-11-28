//go:build integration

/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package integration

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/revisions"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRevision(t *testing.T) {
	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	be := defaultServer.Backend()
	project, err := defaultServer.DefaultProject(context.Background())
	assert.NoError(t, err)

	t.Run("create and list revisions test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))

		// 01. Attach document and make some changes
		assert.NoError(t, c1.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "v1")
			return nil
		}, "add k1"))
		assert.NoError(t, c1.Sync(ctx))

		// 02. Create a revision manually (via backend)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, doc.Key())
		assert.NoError(t, err)

		revision, err := revisions.Create(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			"v1.0",
			"First revision",
		)
		assert.NoError(t, err)
		assert.NotNil(t, revision)
		assert.Equal(t, "v1.0", revision.Label)
		assert.Equal(t, "First revision", revision.Description)

		// 03. Make more changes
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k2", "v2")
			return nil
		}, "add k2"))
		assert.NoError(t, c1.Sync(ctx))

		// 04. Create another revision
		revision2, err := revisions.Create(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			"v2.0",
			"Second revision",
		)
		assert.NoError(t, err)
		assert.NotNil(t, revision2)

		// 05. List all revs
		revs, err := revisions.List(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			types.Paging[int]{
				PageSize:  10,
				Offset:    0,
				IsForward: false,
			},
			false, // don't include snapshot for list
		)
		assert.NoError(t, err)
		assert.Len(t, revs, 2)
		assert.Equal(t, "v2.0", revs[0].Label) // Most recent first
		assert.Equal(t, "v1.0", revs[1].Label)

		// 06. Get revision by ID
		foundRev, err := revisions.Get(ctx, be, revision.ID)
		assert.NoError(t, err)
		assert.Equal(t, revision.ID, foundRev.ID)
		assert.Equal(t, "v1.0", foundRev.Label)

		// 07. Clean up
		assert.NoError(t, c1.Detach(ctx, doc))
	})

	t.Run("delete revision test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))

		// 01. Attach document
		assert.NoError(t, c1.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "v1")
			return nil
		}, "add k1"))
		assert.NoError(t, c1.Sync(ctx))

		// 02. Create a revision
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, doc.Key())
		assert.NoError(t, err)

		revision, err := revisions.Create(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			"to-delete",
			"Revision to be deleted",
		)
		assert.NoError(t, err)

		// 03. Verify revision exists
		foundRevision, err := be.DB.FindRevisionInfoByID(ctx, revision.ID)
		assert.NoError(t, err)
		assert.NotNil(t, foundRevision)

		// 04. Delete the revision
		err = be.DB.DeleteRevisionInfo(ctx, revision.ID)
		assert.NoError(t, err)

		// 05. Verify revision is deleted
		_, err = be.DB.FindRevisionInfoByID(ctx, revision.ID)
		assert.Error(t, err)

		// 06. Clean up
		assert.NoError(t, c1.Detach(ctx, doc))
	})

	t.Run("restore revision test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))

		// 01. Attach document and create initial state
		assert.NoError(t, c1.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "v1")
			r.SetString("k2", "v2")
			return nil
		}, "initial state"))
		assert.NoError(t, c1.Sync(ctx))

		// 02. Create a revision of the initial state
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, doc.Key())
		assert.NoError(t, err)

		revision, err := revisions.Create(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			"v1.0",
			"Initial state",
		)
		assert.NoError(t, err)
		assert.NotEmpty(t, revision.Snapshot)

		// 03. Make more changes
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetString("k1", "modified")
			r.SetString("k3", "v3")
			r.Delete("k2")
			return nil
		}, "modify document"))
		assert.NoError(t, c1.Sync(ctx))

		// 04. Verify the document was modified
		assert.Equal(t, `{"k1":"modified","k3":"v3"}`, doc.Marshal())

		// 05. Restore to the revision
		assert.NoError(t, revisions.Restore(ctx, be, project, revision.ID))

		// 06. Sync to get the restored state
		assert.NoError(t, c1.Sync(ctx))

		// 07. Verify the document was restored to the initial state
		assert.Equal(t, `{"k1":"v1","k2":"v2"}`, doc.Marshal())

		// 08. Clean up
		assert.NoError(t, c1.Detach(ctx, doc))
	})

	t.Run("auto revision creation test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestKey(t))

		// 01. Verify project has AutoRevisionEnabled (default is true)
		projectInfo, err := be.DB.FindProjectInfoByID(ctx, project.ID)
		assert.NoError(t, err)
		assert.True(t, projectInfo.AutoRevisionEnabled)

		// 02. Attach document and make changes
		assert.NoError(t, c1.Attach(ctx, doc))

		// Get initial doc info to check serverSeq changes
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, doc.Key())
		assert.NoError(t, err)

		// Make enough changes to trigger snapshot creation
		// Each Update + Sync creates one change with one operation
		for i := 0; i < int(helper.SnapshotInterval); i++ {
			assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
				r.SetString(fmt.Sprintf("k%d", i), fmt.Sprintf("v%d", i))
				return nil
			}, fmt.Sprintf("add key%d", i)))
			assert.NoError(t, c1.Sync(ctx))
		}

		// 03. Wait a moment for snapshot processing
		time.Sleep(200 * time.Millisecond)

		// 04. Verify serverSeq has increased
		docInfo, err = be.DB.FindDocInfoByKey(ctx, project.ID, doc.Key())
		assert.NoError(t, err)
		assert.Greater(t, docInfo.ServerSeq, helper.SnapshotInterval)

		// 05. Check if auto revisions were created
		revs, err := revisions.List(
			ctx,
			be,
			types.DocRefKey{ProjectID: project.ID, DocID: docInfo.ID},
			types.Paging[int]{
				PageSize:  10,
				Offset:    0,
				IsForward: false,
			},
			false,
		)
		assert.NoError(t, err)

		// 06. Verify at least one auto revision was created
		// Check that auto revision labels start with "auto-"
		foundAutoRevision := false
		for _, rev := range revs {
			if strings.HasPrefix(rev.Label, "auto-") {
				foundAutoRevision = true
				assert.Contains(t, rev.Description, "Auto-created revision")
				break
			}
		}
		assert.True(t, foundAutoRevision, "Should have at least one auto-created revision")

		// 07. Clean up
		assert.NoError(t, c1.Detach(ctx, doc))
	})
}
