//go:build integration

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

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestDeactivateWithMissingDocument covers #2343: a client row that lists a
// document whose row is gone — deleted out of band — used to fail every
// deactivation with "find documents for detachment", so housekeeping retried
// the client every cycle and it never left the activated state.
func TestDeactivateWithMissingDocument(t *testing.T) {
	ctx := context.Background()

	t.Run("deactivate detaches a missing document and the existing ones", func(t *testing.T) {
		clis := activeClients(t, 1)
		c := clis[0]
		// The server deactivates the client below, behind the SDK's back, so
		// the deactivation Close attempts is refused.
		defer func() {
			assert.Equal(t, database.ErrClientNotActivated.Code(), converter.ErrorCodeOf(c.Close()))
		}()

		gone := document.New(helper.TestKey(t, 1))
		require.NoError(t, c.Attach(ctx, gone))
		kept := document.New(helper.TestKey(t, 2))
		require.NoError(t, c.Attach(ctx, kept))

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		require.NoError(t, err)
		goneInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, gone.Key())
		require.NoError(t, err)
		keptInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, kept.Key())
		require.NoError(t, err)

		// Delete the document row out of band, the way an operator purging a
		// document by hand does, without touching the client row.
		_, err = be.DB.PurgeDocument(ctx, goneInfo.RefKey())
		require.NoError(t, err)

		refKey := types.ClientRefKey{ProjectID: project.ID, ClientID: types.IDFromActorID(c.ID())}
		deactivated, err := clients.Deactivate(ctx, be, project, refKey)
		require.NoError(t, err)
		assert.Equal(t, database.ClientDeactivated, deactivated.Status)
		assert.Equal(t, database.DocumentDetached, deactivated.Documents[goneInfo.ID].Status)
		assert.Equal(t, database.DocumentDetached, deactivated.Documents[keptInfo.ID].Status)

		// The existing document was detached through the cluster, not only
		// marked: no client holds it any more.
		attached, err := be.DB.IsDocumentAttachedOrAttaching(ctx, keptInfo.RefKey(), "")
		require.NoError(t, err)
		assert.False(t, attached)

		// The missing one is gone from the client's index arrays too, not only
		// from the documents map.
		attached, err = be.DB.IsDocumentAttachedOrAttaching(ctx, goneInfo.RefKey(), "")
		require.NoError(t, err)
		assert.False(t, attached)
	})

	t.Run("deactivate detaches a missing document left attaching", func(t *testing.T) {
		clis := activeClients(t, 1)
		c := clis[0]
		defer func() {
			assert.Equal(t, database.ErrClientNotActivated.Code(), converter.ErrorCodeOf(c.Close()))
		}()

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		require.NoError(t, err)
		refKey := types.ClientRefKey{ProjectID: project.ID, ClientID: types.IDFromActorID(c.ID())}

		// An attach that never finished, as when the server goes down in the
		// middle of it: the entry stays attaching.
		docInfo, err := be.DB.FindOrCreateDocInfo(ctx, refKey, helper.TestKey(t), false)
		require.NoError(t, err)
		_, err = be.DB.TryAttaching(ctx, refKey, docInfo.ID)
		require.NoError(t, err)

		_, err = be.DB.PurgeDocument(ctx, docInfo.RefKey())
		require.NoError(t, err)

		deactivated, err := clients.Deactivate(ctx, be, project, refKey)
		require.NoError(t, err)
		assert.Equal(t, database.ClientDeactivated, deactivated.Status)
		assert.Equal(t, database.DocumentDetached, deactivated.Documents[docInfo.ID].Status)

		attached, err := be.DB.IsDocumentAttachedOrAttaching(ctx, docInfo.RefKey(), "")
		require.NoError(t, err)
		assert.False(t, attached)
	})
}
