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

package rpc_test

import (
	"context"
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
)

// attachedClientsProbeDB counts, per document, the reads of every client
// attached to it. The sibling check of a server-side detach is the only
// caller on that path.
type attachedClientsProbeDB struct {
	database.Database

	mu    sync.Mutex
	reads map[types.ID]int
}

// newAttachedClientsProbeDB wraps db; every call still goes to db.
func newAttachedClientsProbeDB(db database.Database) *attachedClientsProbeDB {
	return &attachedClientsProbeDB{Database: db, reads: map[types.ID]int{}}
}

// FindAttachedClientInfosByRefKey records the read against the document and
// passes it through.
func (d *attachedClientsProbeDB) FindAttachedClientInfosByRefKey(
	ctx context.Context,
	refKey types.DocRefKey,
) ([]*database.ClientInfo, error) {
	d.mu.Lock()
	d.reads[refKey.DocID]++
	d.mu.Unlock()
	return d.Database.FindAttachedClientInfosByRefKey(ctx, refKey)
}

// readsOf returns how many reads of the document's attached clients were made
// so far.
func (d *attachedClientsProbeDB) readsOf(docID types.ID) int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.reads[docID]
}

// TestServerSideDetachOfPresencelessDocument checks that a server-side detach
// does not look for sibling sessions on a document created with
// disable_presence. The presence clears it would decide on are stripped by
// PushPull anyway, and the check reads every activated client attached to the
// document, which on a busy presenceless document is the whole cost of the
// detach.
func TestServerSideDetachOfPresencelessDocument(t *testing.T) {
	ctx := context.Background()

	// detachResult is what one server-side detach did to its document.
	type detachResult struct {
		// attachedClientReads counts the reads of the document's attached
		// clients made by the detach alone, not by the attach before it.
		attachedClientReads int
		serverSeqBefore     int64
		serverSeqAfter      int64
	}

	// deactivateWithoutDetach attaches one client to a fresh document and
	// deactivates it without detaching, so the server detaches on its behalf.
	deactivateWithoutDetach := func(t *testing.T, disablePresence bool) detachResult {
		activateResp, err := testClient.ActivateClient(ctx, connect.NewRequest(
			&api.ActivateClientRequest{ClientKey: t.Name()},
		))
		require.NoError(t, err)
		clientID := activateResp.Msg.ClientId

		attachResp, err := testClient.AttachDocument(ctx, connect.NewRequest(
			&api.AttachDocumentRequest{
				ClientId: clientID,
				ChangePack: &api.ChangePack{
					DocumentKey: helper.TestKey(t).String(),
					Checkpoint:  &api.Checkpoint{ServerSeq: 0, ClientSeq: 0},
				},
				DisablePresence: disablePresence,
			},
		))
		require.NoError(t, err)
		require.Equal(t, disablePresence, attachResp.Msg.DisablePresence)
		docID := types.ID(attachResp.Msg.DocumentId)
		refKey := types.DocRefKey{ProjectID: database.DefaultProjectID, DocID: docID}

		before, err := testBackend.DB.FindDocInfoByRefKey(ctx, refKey)
		require.NoError(t, err)
		readsBefore := testProbeDB.readsOf(docID)

		_, err = testClient.DeactivateClient(ctx, connect.NewRequest(
			&api.DeactivateClientRequest{ClientId: clientID, Synchronous: true},
		))
		require.NoError(t, err)

		after, err := testBackend.DB.FindDocInfoByRefKey(ctx, refKey)
		require.NoError(t, err)

		// DeactivateClient only succeeds once no document is attached, so a
		// deactivated row means the detach went through.
		info, err := testBackend.DB.FindClientInfoByRefKey(ctx, types.ClientRefKey{
			ProjectID: database.DefaultProjectID,
			ClientID:  types.ID(clientID),
		}, true)
		require.NoError(t, err)
		assert.Equal(t, database.ClientDeactivated, info.Status)
		require.Contains(t, info.Documents, docID)
		assert.Equal(t, database.DocumentDetached, info.Documents[docID].Status)

		return detachResult{
			attachedClientReads: testProbeDB.readsOf(docID) - readsBefore,
			serverSeqBefore:     before.ServerSeq,
			serverSeqAfter:      after.ServerSeq,
		}
	}

	t.Run("presenceless document skips the sibling check", func(t *testing.T) {
		r := deactivateWithoutDetach(t, true)
		assert.Equal(t, 0, r.attachedClientReads)
		// Nothing reaches the document: the clears it used to build were
		// stripped by PushPull, and now none are built.
		assert.Equal(t, r.serverSeqBefore, r.serverSeqAfter)
	})

	t.Run("document with presence still runs the sibling check", func(t *testing.T) {
		// Shows the probe sees the read the presenceless case skips, and that
		// the clears are stored where presence is kept.
		r := deactivateWithoutDetach(t, false)
		assert.Equal(t, 1, r.attachedClientReads)
		assert.Greater(t, r.serverSeqAfter, r.serverSeqBefore)
	})
}
