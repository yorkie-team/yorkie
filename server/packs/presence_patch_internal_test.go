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
	gotime "time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func newCacheBackend(t *testing.T) *backend.Backend {
	t.Helper()
	manager, err := cache.New(cache.Options{
		AuthWebhookCacheSize:         1,
		AuthWebhookCacheTTL:          gotime.Second,
		SnapshotCacheSize:            1,
		PresenceBaseCacheSize:        8,
		ChannelSessionCountCacheSize: 1,
		ChannelSessionCountCacheTTL:  gotime.Second,
	})
	require.NoError(t, err)
	return &backend.Backend{Cache: manager}
}

// clientAt returns a client whose checkpoint in docKey's document is at
// (serverSeq, clientSeq) in epoch 1.
func clientAt(docKey types.DocRefKey, serverSeq int64, clientSeq uint32) *database.ClientInfo {
	return &database.ClientInfo{
		ID: types.ID(sessionActorHex),
		Documents: database.ClientDocInfoMap{
			docKey.DocID: {
				Status:    database.DocumentAttached,
				ServerSeq: serverSeq,
				ClientSeq: clientSeq,
				Epoch:     1,
			},
		},
	}
}

func put(data presence.Data) *presence.Change {
	return &presence.Change{ChangeType: presence.Put, Presence: data}
}

func patch(data presence.Data, removed ...string) *presence.Change {
	return &presence.Change{ChangeType: presence.Patch, Presence: data, RemovedKeys: removed}
}

func clearChange() *presence.Change {
	return &presence.Change{ChangeType: presence.Clear}
}

// presencePack builds a pack of presence-only changes numbered from
// firstClientSeq; a nil entry is a change without presence.
func presencePack(firstClientSeq uint32, pcs ...*presence.Change) *change.Pack {
	var changes []*change.Change
	for i, pc := range pcs {
		id := change.NewID(firstClientSeq+uint32(i), 0, 0, time.InitialActorID, time.NewVersionVector())
		changes = append(changes, change.New(id, "", nil, pc))
	}
	return change.NewPack(key.Key("doc"), change.InitialCheckpoint, changes, nil, nil)
}

// stored is what pushPack would store from the pack: every change above the
// client's checkpoint.
func stored(t *testing.T, docKey types.DocRefKey, cp change.Checkpoint, pack *change.Pack) []*database.ChangeInfo {
	t.Helper()
	var infos []*database.ChangeInfo
	for _, cn := range pack.Changes {
		if cn.ClientSeq() <= cp.ClientSeq {
			continue
		}
		info, err := database.NewFromChange(docKey, cn)
		require.NoError(t, err)
		infos = append(infos, info)
	}
	return infos
}

// pullTo moves the client's checkpoint the way pullPack does after a push.
func pullTo(clientInfo *database.ClientInfo, docID types.ID, serverSeq int64, clientSeq uint32) {
	clientInfo.Documents[docID].ServerSeq = serverSeq
	clientInfo.Documents[docID].ClientSeq = clientSeq
}

func TestFoldPresencePatches(t *testing.T) {
	docKey := types.DocRefKey{ProjectID: types.ID(sessionActorHex), DocID: types.ID(stableActorHex)}
	baseKey := cache.PresenceBaseKey{DocRefKey: docKey, ClientID: types.ID(sessionActorHex)}
	cached := func(serverSeq int64, clientSeq uint32, data presence.Data) cache.PresenceBase {
		return cache.PresenceBase{Checkpoint: change.NewCheckpoint(serverSeq, clientSeq), Epoch: 1, Presence: data}
	}

	t.Run("a patch after a put in the same pack is folded", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 0, 0)
		pack := presencePack(1,
			put(presence.Data{"name": "a", "selection": "x"}),
			patch(presence.Data{"cursor": "1"}, "selection"),
		)

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		assert.Equal(t, put(presence.Data{"name": "a", "cursor": "1"}), pack.Changes[1].PresenceChange())

		pushed := stored(t, docKey, change.InitialCheckpoint, pack)
		pullTo(clientInfo, docKey.DocID, 2, 2)
		fold.commit(be, clientInfo, docKey.DocID, pushed)
		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, cached(2, 2, presence.Data{"name": "a", "cursor": "1"}), got)
	})

	t.Run("a patch is folded onto the base cached at the client's checkpoint", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, presence.Data{"name": "a"}))

		pack := presencePack(4, patch(presence.Data{"cursor": "1"}))
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)
		assert.Equal(t, put(presence.Data{"name": "a", "cursor": "1"}), pack.Changes[0].PresenceChange())
	})

	t.Run("a patch without a cached base is refused", func(t *testing.T) {
		be := newCacheBackend(t)
		_, err := foldPresencePatches(
			be, clientAt(docKey, 0, 0), docKey,
			presencePack(1, patch(presence.Data{"cursor": "1"})),
		)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	// The client's previous push went through another server, so the cached
	// presence may be missing whatever that push changed.
	t.Run("a base cached at an older client seq is not used", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 2, presence.Data{"name": "a"}))

		_, err := foldPresencePatches(
			be, clientAt(docKey, 7, 3), docKey,
			presencePack(4, patch(presence.Data{"cursor": "1"})),
		)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	// The client detached and re-attached through another server: its client
	// seq starts over and can land on the cached one again, but the document's
	// server seq has moved on.
	t.Run("a base from an earlier attachment is not used", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))

		_, err := foldPresencePatches(
			be, clientAt(docKey, 9, 1), docKey,
			presencePack(2, patch(presence.Data{"cursor": "1"})),
		)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	t.Run("a base from another epoch is not used", func(t *testing.T) {
		be := newCacheBackend(t)
		base := cached(7, 3, presence.Data{"name": "a"})
		base.Epoch = 0
		be.Cache.PresenceBase.Add(baseKey, base)

		_, err := foldPresencePatches(
			be, clientAt(docKey, 7, 3), docKey,
			presencePack(4, patch(presence.Data{"cursor": "1"})),
		)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	// A detach pack ends with a clear, so a pending patch it carries has no
	// effect either way and must not fail the detach.
	t.Run("a patch without a base is dropped when a later put or clear replaces it", func(t *testing.T) {
		be := newCacheBackend(t)
		pack := presencePack(1, patch(presence.Data{"cursor": "1"}), clearChange())

		_, err := foldPresencePatches(be, clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, pack.Changes[0].PresenceChange())
		assert.Equal(t, clearChange(), pack.Changes[1].PresenceChange())
	})

	t.Run("a patch after a clear is refused", func(t *testing.T) {
		be := newCacheBackend(t)
		pack := presencePack(1, put(presence.Data{"name": "a"}), clearChange(), patch(presence.Data{"cursor": "1"}))
		_, err := foldPresencePatches(be, clientAt(docKey, 0, 0), docKey, pack)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	t.Run("already pushed changes are skipped", func(t *testing.T) {
		be := newCacheBackend(t)
		pack := presencePack(1, patch(presence.Data{"cursor": "1"}))

		fold, err := foldPresencePatches(be, clientAt(docKey, 1, 1), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, fold)
		assert.Equal(t, presence.Patch, pack.Changes[0].PresenceChange().ChangeType)
	})

	// Pulls move the checkpoint too, so the base follows them on this server.
	t.Run("a pull without changes moves the base to the new checkpoint", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 3)

		fold, err := foldPresencePatches(be, clientInfo, docKey, presencePack(4))
		require.NoError(t, err)
		pullTo(clientInfo, docKey.DocID, 12, 3)
		fold.commit(be, clientInfo, docKey.DocID, nil)

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, cached(12, 3, presence.Data{"name": "a"}), got)
	})

	t.Run("a clear drops the cached base", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)
		pack := presencePack(2, clearChange())

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		fold.commit(be, clientInfo, docKey.DocID, stored(t, docKey, change.NewCheckpoint(7, 1), pack))
		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	// A stale epoch or the size gate discarded the changes, so the presence
	// on the server is still the one from before the push.
	t.Run("a push whose changes were discarded keeps the earlier base", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)

		fold, err := foldPresencePatches(be, clientInfo, docKey, presencePack(2, put(presence.Data{"name": "b"})))
		require.NoError(t, err)
		fold.commit(be, clientInfo, docKey.DocID, nil)

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, presence.Data{"name": "a"}, got.Presence)
	})

	t.Run("the cached base is not aliased to the pushed change", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 0, 0)
		data := presence.Data{"name": "a"}
		pack := presencePack(1, put(data))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		fold.commit(be, clientInfo, docKey.DocID, stored(t, docKey, change.InitialCheckpoint, pack))

		data["name"] = "changed"
		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, presence.Data{"name": "a"}, got.Presence)
	})
}
