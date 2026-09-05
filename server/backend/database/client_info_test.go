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

package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestClientInfo(t *testing.T) {
	dummyDocID := types.ID("000000000000000000000000")
	dummyProjectID := types.ID("000000000000000000000000")
	otherProjectID := types.ID("000000000000000000000001")

	t.Run("attach/detach document test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.UpdateCheckpoint(dummyDocID, change.MaxCheckpoint)
		assert.NoError(t, err)

		err = clientInfo.EnsureDocumentAttached(dummyDocID)
		assert.NoError(t, err)

		err = clientInfo.DetachDocument(dummyDocID)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.False(t, isAttached)

		err = clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)
		isAttached, err = clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

	})

	t.Run("attach seeds from presented checkpoint test", func(t *testing.T) {
		// Case B (reload / new session): a non-zero presented checkpoint is the
		// resume signal and seeds ClientDocInfo so restored un-pushed changes
		// push from the right clientSeq. With no presented epoch (0), the epoch
		// falls back to the current doc epoch.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		presented := change.NewCheckpoint(7, 5)
		err := clientInfo.AttachDocument(dummyDocID, false, 3, 0, presented)
		assert.NoError(t, err)

		cp := clientInfo.Checkpoint(dummyDocID)
		assert.Equal(t, int64(7), cp.ServerSeq)
		assert.Equal(t, uint32(5), cp.ClientSeq)
		assert.Equal(t, int64(3), clientInfo.Documents[dummyDocID].Epoch)
	})

	t.Run("attach seeds presented epoch on resume test", func(t *testing.T) {
		// Case B resume with a stale presented epoch (Q2 pull-before-trust): the
		// client's persisted epoch (2) is seeded verbatim, not the current doc
		// epoch (5). The seeded old epoch differs from the doc epoch, so the
		// epoch check in pushpull fires ErrEpochMismatch and re-anchors from a
		// snapshot. This is the signal that survives a force-compaction that
		// happened while the client was offline.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		presented := change.NewCheckpoint(7, 5)
		err := clientInfo.AttachDocument(dummyDocID, false, 5, 2, presented)
		assert.NoError(t, err)

		assert.Equal(t, int64(2), clientInfo.Documents[dummyDocID].Epoch)
	})

	t.Run("fresh attach without presented epoch seeds doc epoch test", func(t *testing.T) {
		// A fresh attach that presents no epoch (0) seeds the current doc epoch.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		err := clientInfo.AttachDocument(dummyDocID, false, 5, 0, change.InitialCheckpoint)
		assert.NoError(t, err)

		assert.Equal(t, int64(5), clientInfo.Documents[dummyDocID].Epoch)
	})

	t.Run("empty-root resume seeds presented epoch despite zero serverSeq test", func(t *testing.T) {
		// A doc force-compacted to an EMPTY root sits at ServerSeq 0, so the
		// over-claim clamp drives the presented ServerSeq to 0 even for a genuine
		// resume. The presented epoch is an independent resume signal: it is
		// seeded (2) rather than the current doc epoch (5) whenever non-zero, so
		// the epoch check downstream fires ErrEpochMismatch. The checkpoint still
		// seeds 0/0 because ServerSeq is 0.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		err := clientInfo.AttachDocument(dummyDocID, false, 5, 2, change.InitialCheckpoint)
		assert.NoError(t, err)

		assert.Equal(t, int64(2), clientInfo.Documents[dummyDocID].Epoch)
		cp := clientInfo.Checkpoint(dummyDocID)
		assert.Equal(t, int64(0), cp.ServerSeq)
		assert.Equal(t, uint32(0), cp.ClientSeq)
	})

	t.Run("attach seeds zero for fresh presented checkpoint test", func(t *testing.T) {
		// Fresh attach (presented == InitialCheckpoint): today's behavior,
		// seed 0/0.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)

		cp := clientInfo.Checkpoint(dummyDocID)
		assert.Equal(t, int64(0), cp.ServerSeq)
		assert.Equal(t, uint32(0), cp.ClientSeq)
	})

	t.Run("attach with local edits but zero serverSeq seeds zero test", func(t *testing.T) {
		// A fresh attach carrying local edits presents a non-zero ClientSeq with
		// ServerSeq 0. That is not a resume: the resume signal is the ServerSeq.
		// Seeding a non-zero ClientSeq here would make pushpull drop the pending
		// changes as already-pushed, so it must seed 0/0.
		clientInfo := database.ClientInfo{Status: database.ClientActivated}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.NewCheckpoint(0, 2))
		assert.NoError(t, err)

		cp := clientInfo.Checkpoint(dummyDocID)
		assert.Equal(t, int64(0), cp.ServerSeq)
		assert.Equal(t, uint32(0), cp.ClientSeq)
	})

	t.Run("check if in project test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			ProjectID: dummyProjectID,
		}

		err := clientInfo.CheckIfInProject(dummyProjectID)
		assert.NoError(t, err)
	})

	t.Run("check if in project error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			ProjectID: dummyProjectID,
		}

		err := clientInfo.CheckIfInProject(otherProjectID)
		assert.ErrorIs(t, err, database.ErrClientNotFound)
	})

	t.Run("client deactivate test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		clientInfo.Deactivate()

		err = clientInfo.EnsureDocumentAttached(dummyDocID)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)
	})

	t.Run("client not activate error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientDeactivated,
		}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)

		err = clientInfo.EnsureDocumentAttached(dummyDocID)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)

		err = clientInfo.DetachDocument(dummyDocID)
		assert.ErrorIs(t, err, database.ErrClientNotActivated)
	})

	t.Run("document not attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}
		err := clientInfo.DetachDocument(dummyDocID)
		assert.ErrorIs(t, err, database.ErrDocumentNotAttached)
	})

	t.Run("document never attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}
		_, err := clientInfo.IsAttached(dummyDocID)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)

		err = clientInfo.UpdateCheckpoint(dummyDocID, change.MaxCheckpoint)
		assert.ErrorIs(t, err, database.ErrDocumentNeverAttached)
	})

	t.Run("document already attached error test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		err = clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.ErrorIs(t, err, database.ErrDocumentAlreadyAttached)
	})

	t.Run("is own actor compare-both test", func(t *testing.T) {
		sessionID := types.ID("0000000000000000000000aa")
		stableID := types.ID("0000000000000000000000bb")
		thirdParty := types.ID("0000000000000000000000cc")

		// New SDK: both session id and stable actor are recognized as own.
		newClient := database.ClientInfo{ID: sessionID, StableActorID: stableID}
		assert.True(t, newClient.IsOwnActor(sessionID))
		assert.True(t, newClient.IsOwnActor(stableID))
		assert.False(t, newClient.IsOwnActor(thirdParty))

		// Old SDK / pre-Phase-1 row: empty StableActorID must never match, so
		// only the session id is recognized as own.
		oldClient := database.ClientInfo{ID: sessionID}
		assert.True(t, oldClient.IsOwnActor(sessionID))
		assert.False(t, oldClient.IsOwnActor(stableID))
		assert.False(t, oldClient.IsOwnActor(thirdParty))
		// An empty StableActorID must not match an empty query actor either.
		assert.False(t, oldClient.IsOwnActor(types.ID("")))
	})

	t.Run("own actor id prefers stable actor test", func(t *testing.T) {
		sessionID := types.IDFromActorID(time.MaxActorID)
		stableID := types.DeriveActorID(dummyProjectID, "own-actor-key")

		// New SDK: OwnActorID returns the stable actor.
		newClient := database.ClientInfo{ID: sessionID, StableActorID: stableID}
		actorID, err := newClient.OwnActorID()
		assert.NoError(t, err)
		stableActor, err := stableID.ToActorID()
		assert.NoError(t, err)
		assert.Equal(t, stableActor, actorID)

		// Old SDK / pre-Phase-1 row: OwnActorID falls back to the session id.
		oldClient := database.ClientInfo{ID: sessionID}
		actorID, err = oldClient.OwnActorID()
		assert.NoError(t, err)
		sessionActor, err := sessionID.ToActorID()
		assert.NoError(t, err)
		assert.Equal(t, sessionActor, actorID)
	})

	t.Run("dedup path recognizes stable-actor own changes test", func(t *testing.T) {
		sessionID := types.ID("0000000000000000000000aa")
		stableID := types.DeriveActorID(dummyProjectID, "dedup-key")
		thirdParty := types.DeriveActorID(dummyProjectID, "third-party-key")

		// New SDK stamps the stable actor into its changes.
		clientInfo := database.ClientInfo{ID: sessionID, StableActorID: stableID}

		// A pulled change carrying the client's stable actor, already acked by
		// the checkpoint, is this client's own and must be deduped.
		ownChange := &database.ChangeInfo{ActorID: stableID, ClientSeq: 3}
		// A change from another client is never this client's own.
		otherChange := &database.ChangeInfo{ActorID: thirdParty, ClientSeq: 3}

		// Mirror the filter condition in packs.pullChangeInfos: a change is
		// dropped when it is the client's own AND the checkpoint already
		// covers its clientSeq.
		cpClientSeq := uint32(5)
		dedup := func(ci *database.ChangeInfo) bool {
			return clientInfo.IsOwnActor(ci.ActorID) && cpClientSeq >= ci.ClientSeq
		}
		assert.True(t, dedup(ownChange), "stable-actor own change must dedup")
		assert.False(t, dedup(otherChange), "third-party change must not dedup")

		// An old-SDK client whose changes carry the session id must still
		// dedup its own changes and never dedup a stable-actor stranger.
		oldClient := database.ClientInfo{ID: sessionID}
		oldOwn := &database.ChangeInfo{ActorID: sessionID, ClientSeq: 3}
		assert.True(t, oldClient.IsOwnActor(oldOwn.ActorID))
		assert.False(t, oldClient.IsOwnActor(stableID))
	})

	t.Run("document detached when client deactivate test", func(t *testing.T) {
		clientInfo := database.ClientInfo{
			Status: database.ClientActivated,
		}

		err := clientInfo.AttachDocument(dummyDocID, false, 0, 0, change.InitialCheckpoint)
		assert.NoError(t, err)
		isAttached, err := clientInfo.IsAttached(dummyDocID)
		assert.NoError(t, err)
		assert.True(t, isAttached)

		clientInfo.Deactivate()

		err = clientInfo.EnsureDocumentsNotAttachedWhenDeactivated()
		assert.Equal(t, database.ErrAttachedDocumentExists, err)
	})
}
