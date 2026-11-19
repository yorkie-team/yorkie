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
	"fmt"
	"strconv"
	"strings"
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

// cursorWidth is the width every value patches below carries, so each fold of
// a run costs the same however far into the run it is.
const cursorWidth = 2

// cursor is the value the nth patch of a run carries. The values are distinct,
// so a put the fold keeps names which patch produced it: a walk that stopped
// folding into the base part way through a coalesced run would keep an earlier
// one than the sender's own.
func cursor(n int) string {
	return fmt.Sprintf("%0*d", cursorWidth, n)
}

// maxFoldBase returns a presence that the patches below fold into a put of
// exactly maxPresenceBaseSize, so the fold budget pays for exactly
// maxPresenceFoldPerPack / maxPresenceBaseSize of them.
func maxFoldBase() presence.Data {
	size := maxPresenceBaseSize - len("blob") - len("cursor") - cursorWidth - 2*presenceEntryOverhead
	return presence.Data{"blob": strings.Repeat("x", size)}
}

// patches returns n patches of the one key maxFoldBase leaves room for, each
// carrying a different value.
func patches(n int) []*presence.Change {
	var pcs []*presence.Change
	for i := range n {
		pcs = append(pcs, patch(presence.Data{"cursor": cursor(i)}))
	}
	return pcs
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

// storedAs reads the client back as the given one, the way the store returns
// it when the reply and the stored checkpoint agree.
func storedAs(clientInfo *database.ClientInfo) func() (*database.ClientInfo, error) {
	return func() (*database.ClientInfo, error) { return clientInfo, nil }
}

// afterPush is the checkpoint this request's own push produced. commit only
// compares its client seq with the stored one, so that is all it carries.
func afterPush(clientSeq uint32) change.Checkpoint {
	return change.NewCheckpoint(0, clientSeq)
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
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2), pushed)
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
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(3), nil)

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, cached(12, 3, presence.Data{"name": "a"}), got)
	})

	// A push-only reply returns the server seq the client sent, which can be
	// behind the one the store kept from the client's last pull. The next push
	// reads the stored one, so that is what the base has to be keyed to.
	t.Run("a base is keyed to the stored checkpoint, not the reply's", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(12, 3, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 12, 3)
		pack := presencePack(4, patch(presence.Data{"cursor": "1"}))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		// The push-only reply carried (7, 4); the store kept (12, 4).
		pullTo(clientInfo, docKey.DocID, 7, 4)
		fold.commit(be, storedAs(clientAt(docKey, 12, 4)), docKey.DocID, afterPush(4),
			stored(t, docKey, change.NewCheckpoint(12, 3), pack))

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, change.NewCheckpoint(12, 4), got.Checkpoint)
	})

	// The pull lock is per-process, so a concurrent push by the same client on
	// another server can advance the stored client seq between the pull and
	// the read-back. That push carries presence this request never saw, so
	// keying this request's presence to the newer checkpoint would let the
	// client's next patch fold onto presence the store has moved past.
	t.Run("a base is dropped when the stored client seq moved past this push", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)
		pack := presencePack(2, put(presence.Data{"name": "b"}))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.NewCheckpoint(7, 1), pack)
		// This request pushed up to client seq 2; the store already holds 3.
		fold.commit(be, storedAs(clientAt(docKey, 9, 3)), docKey.DocID, afterPush(2), pushed)

		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	// Keying the base to a checkpoint the store may not hold could match a
	// later attachment, so a failed read drops it instead.
	t.Run("a base is dropped when the stored client cannot be read", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)
		pack := presencePack(2, put(presence.Data{"name": "b"}))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		fold.commit(be, func() (*database.ClientInfo, error) {
			return nil, database.ErrClientNotFound
		}, docKey.DocID, afterPush(2), stored(t, docKey, change.NewCheckpoint(7, 1), pack))
		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	// Nothing is kept, so the store is not read.
	t.Run("a clear does not read the stored client", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)
		pack := presencePack(2, clearChange())

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		fold.commit(be, func() (*database.ClientInfo, error) {
			t.Fatal("stored client read for a clear")
			return nil, nil
		}, docKey.DocID, afterPush(2), stored(t, docKey, change.NewCheckpoint(7, 1), pack))
		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	t.Run("a clear drops the cached base", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 1, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 1)
		pack := presencePack(2, clearChange())

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2),
			stored(t, docKey, change.NewCheckpoint(7, 1), pack))
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
		// Nothing was stored, so the push did not forward the client seq.
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(1), nil)

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, presence.Data{"name": "a"}, got.Presence)
	})

	// DetachDocument zeroes the client's checkpoint to the (0, 0) a fresh
	// attach seeds, so a base kept past the detach would pass the freshness
	// check on the next attachment to this server.
	t.Run("a base is not kept past a detach", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 7, 1)
		clientInfo.Status = database.ClientActivated
		pack := presencePack(2, put(presence.Data{"name": "a"}))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.NewCheckpoint(7, 1), pack)

		// pullPack detaches the client before commit runs.
		require.NoError(t, clientInfo.DetachDocument(docKey.DocID))
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2), pushed)
		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	t.Run("a base cached at the seeded checkpoint is not used", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(0, 0, presence.Data{"name": "a"}))

		_, err := foldPresencePatches(
			be, clientAt(docKey, 0, 0), docKey,
			presencePack(1, patch(presence.Data{"cursor": "1"})),
		)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	// Presence is excluded from every document size gate, so the cache is
	// what bounds it: an entry too large to hold is dropped instead.
	t.Run("a presence over the size bound is not cached", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 7, 1)
		big := presence.Data{"blob": strings.Repeat("x", maxPresenceBaseSize+1)}
		pack := presencePack(2, put(big))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.NewCheckpoint(7, 1), pack)
		pullTo(clientInfo, docKey.DocID, 8, 2)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2), pushed)

		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	t.Run("a presence within the size bound is cached", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 7, 1)
		data := presence.Data{"blob": strings.Repeat("x", maxPresenceBaseSize-len("blob")-presenceEntryOverhead)}
		pack := presencePack(2, put(data))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.NewCheckpoint(7, 1), pack)
		pullTo(clientInfo, docKey.DocID, 8, 2)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2), pushed)

		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, maxPresenceBaseSize, presenceSize(got.Presence))
	})

	// The put a patch is folded into is stored and fanned out, so the fold is
	// what bounds how much a request can cost beyond its own bytes.
	t.Run("a patch whose fold would pass the size bound is refused", func(t *testing.T) {
		be := newCacheBackend(t)
		big := presence.Data{"blob": strings.Repeat("x", maxPresenceBaseSize-len("blob")-presenceEntryOverhead)}
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, big))

		pack := presencePack(4, patch(presence.Data{"cursor": "1"}))
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
		assert.Equal(t, presence.Patch, pack.Changes[0].PresenceChange().ChangeType)
	})

	// Each folded put is stored and fanned out, so a pack of minimal patches
	// would otherwise cost one full presence per change however small the
	// request was. Refusing the push instead would not converge: the client
	// is told only that the base was unavailable, so a resend that replaced
	// one patch with a put would be refused again, operations included.
	t.Run("patches over the fold budget share one put and keep the push", func(t *testing.T) {
		be := newCacheBackend(t)
		base := maxFoldBase()
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, base))

		within := maxPresenceFoldPerPack / maxPresenceBaseSize
		pack := presencePack(4, patches(within+3)...)
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)

		// Each put the fold keeps carries the presence of the patch it sits
		// on, the last one of the run included: the run drops the presences
		// between them, not the one the sender ends up holding.
		total := 0
		for i, cn := range pack.Changes {
			pc := cn.PresenceChange()
			if i < within || i == len(pack.Changes)-1 {
				folded := put(presence.Data{"blob": base["blob"], "cursor": cursor(i)})
				assert.Equal(t, folded, pc, "change %d", i)
				total += presenceSize(pc.Presence)
				continue
			}
			assert.Nil(t, pc, "change %d", i)
		}
		// However many patches the pack carries, its folds cost the budget
		// plus the one put the run past it keeps.
		assert.LessOrEqual(t, total, maxPresenceFoldPerPack+maxPresenceBaseSize)
	})

	// A put the client sent is paid for in its own bytes, so the run of
	// patches after it keeps a put of its own.
	t.Run("a put past the budget starts a new coalesced run", func(t *testing.T) {
		be := newCacheBackend(t)
		base := maxFoldBase()
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, base))

		within := maxPresenceFoldPerPack / maxPresenceBaseSize
		pcs := append(patches(within+2), put(presence.Data{"name": "a"}))
		pack := presencePack(4, append(pcs, patches(2)...)...)
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)

		// The run before the put keeps no put of its own: the put replaces
		// the presence anyway. The run after it folds onto what the put
		// carried and keeps its last fold.
		assert.Nil(t, pack.Changes[within+1].PresenceChange())
		assert.Equal(t, put(presence.Data{"name": "a"}), pack.Changes[within+2].PresenceChange())
		assert.Nil(t, pack.Changes[within+3].PresenceChange())
		assert.Equal(t, put(presence.Data{"name": "a", "cursor": cursor(1)}),
			pack.Changes[len(pack.Changes)-1].PresenceChange())
	})

	t.Run("a pack of patches within the fold budget is folded", func(t *testing.T) {
		be := newCacheBackend(t)
		base := maxFoldBase()
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, base))

		pack := presencePack(4, patches(maxPresenceFoldPerPack/maxPresenceBaseSize)...)
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)
		for i, cn := range pack.Changes {
			folded := presence.Data{"blob": base["blob"], "cursor": cursor(i)}
			assert.Equal(t, put(folded), cn.PresenceChange(), "change %d", i)
		}
	})

	// A put is paid for in the bytes the client sent, so only the puts the
	// fold creates are charged against the budget.
	t.Run("the fold budget counts folded puts only", func(t *testing.T) {
		be := newCacheBackend(t)
		base := maxFoldBase()
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, base))

		var pcs []*presence.Change
		for range maxPresenceFoldPerPack/maxPresenceBaseSize + 1 {
			pcs = append(pcs, put(base))
		}
		// Two patches, not one: a budget that charged the puts would coalesce
		// the run they form and write its single put on the last of them, which
		// is the put a single trailing patch would be given either way.
		pack := presencePack(4, append(pcs, patches(2)...)...)

		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)
		n := len(pack.Changes)
		assert.Equal(t,
			put(presence.Data{"blob": base["blob"], "cursor": cursor(0)}),
			pack.Changes[n-2].PresenceChange(),
		)
		assert.Equal(t,
			put(presence.Data{"blob": base["blob"], "cursor": cursor(1)}),
			pack.Changes[n-1].PresenceChange(),
		)
	})

	// A detach pack ends with a clear, so passing the budget must not fail it
	// any more than a missing base does.
	t.Run("a patch over the fold budget is dropped when a later clear replaces it", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, maxFoldBase()))

		over := maxPresenceFoldPerPack/maxPresenceBaseSize + 1
		pack := presencePack(4, append(patches(over), clearChange())...)
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, pack.Changes[over-1].PresenceChange())
		assert.Equal(t, clearChange(), pack.Changes[over].PresenceChange())
	})

	t.Run("a patch folded within the size bound is stored as a put", func(t *testing.T) {
		be := newCacheBackend(t)
		blob := strings.Repeat("x", maxPresenceBaseSize-len("blob")-len("cursor")-len("1")-2*presenceEntryOverhead)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, presence.Data{"blob": blob}))

		pack := presencePack(4, patch(presence.Data{"cursor": "1"}))
		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		require.NoError(t, err)
		assert.Equal(t, put(presence.Data{"blob": blob, "cursor": "1"}), pack.Changes[0].PresenceChange())
	})

	// Nothing else bounds the key count, and a map entry costs far more than
	// the two bytes a one-byte key and value weigh.
	t.Run("a presence of many tiny keys is not cached", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 7, 1)
		many := presence.Data{}
		for i := range maxPresenceBaseSize/presenceEntryOverhead + 1 {
			many[strconv.Itoa(i)] = "x"
		}
		pack := presencePack(2, put(many))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.NewCheckpoint(7, 1), pack)
		pullTo(clientInfo, docKey.DocID, 8, 2)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(2), pushed)

		assert.False(t, be.Cache.PresenceBase.Contains(baseKey))
	})

	t.Run("the cached base is not aliased to the pushed change", func(t *testing.T) {
		be := newCacheBackend(t)
		clientInfo := clientAt(docKey, 0, 0)
		data := presence.Data{"name": "a"}
		pack := presencePack(1, put(data))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pushed := stored(t, docKey, change.InitialCheckpoint, pack)
		pullTo(clientInfo, docKey.DocID, 1, 1)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(1), pushed)

		data["name"] = "changed"
		got, ok := be.Cache.PresenceBase.Get(baseKey)
		require.True(t, ok)
		assert.Equal(t, presence.Data{"name": "a"}, got.Presence)
	})
}

// sizedPresence returns a one-key presence of exactly size, as presenceSize
// measures it.
func sizedPresence(size int) presence.Data {
	return presence.Data{"blob": strings.Repeat("x", size-len("blob")-presenceEntryOverhead)}
}

func TestPresencePutBounds(t *testing.T) {
	docKey := types.DocRefKey{ProjectID: types.ID(sessionActorHex), DocID: types.ID(stableActorHex)}
	baseKey := cache.PresenceBaseKey{DocRefKey: docKey, ClientID: types.ID(sessionActorHex)}
	cached := func(serverSeq int64, clientSeq uint32, data presence.Data) cache.PresenceBase {
		return cache.PresenceBase{Checkpoint: change.NewCheckpoint(serverSeq, clientSeq), Epoch: 1, Presence: data}
	}

	t.Run("a put at the bound is stored as sent", func(t *testing.T) {
		data := sizedPresence(maxPresencePutSize)
		pack := presencePack(1, put(data))

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		assert.Equal(t, put(data), pack.Changes[0].PresenceChange())
	})

	t.Run("a put over the bound loses its presence and keeps the push", func(t *testing.T) {
		pack := presencePack(1, put(sizedPresence(maxPresencePutSize+1)))

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, pack.Changes[0].PresenceChange())
	})

	// Nothing else bounds the key count, and a map entry costs far more than
	// the two bytes a one-byte key and value weigh.
	t.Run("a put of many tiny keys loses its presence", func(t *testing.T) {
		many := presence.Data{}
		for i := range maxPresencePutSize/presenceEntryOverhead + 1 {
			many[strconv.Itoa(i)] = "x"
		}
		pack := presencePack(1, put(many))

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, pack.Changes[0].PresenceChange())
	})

	// The sender's presence moved on with a put the server did not store, so
	// the presence it held from before that put is no longer the sender's.
	t.Run("a put over the bound drops the base instead of leaving a stale one", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, presence.Data{"name": "a"}))
		clientInfo := clientAt(docKey, 7, 3)
		cp := change.NewCheckpoint(7, 3)
		pack := presencePack(4, put(sizedPresence(maxPresencePutSize+1)))

		fold, err := foldPresencePatches(be, clientInfo, docKey, pack)
		require.NoError(t, err)
		pullTo(clientInfo, docKey.DocID, 8, 4)
		fold.commit(be, storedAs(clientInfo), docKey.DocID, afterPush(4), stored(t, docKey, cp, pack))

		_, ok := be.Cache.PresenceBase.Get(baseKey)
		assert.False(t, ok)
	})

	// Folding the next patch onto the presence from before the dropped put
	// would store a presence the sender never held.
	t.Run("a patch after a put over the bound is refused", func(t *testing.T) {
		be := newCacheBackend(t)
		be.Cache.PresenceBase.Add(baseKey, cached(7, 3, presence.Data{"name": "a"}))
		pack := presencePack(4,
			put(sizedPresence(maxPresencePutSize+1)),
			patch(presence.Data{"cursor": "1"}),
		)

		_, err := foldPresencePatches(be, clientAt(docKey, 7, 3), docKey, pack)
		assert.ErrorIs(t, err, ErrPresenceBaseUnavailable)
	})

	// Only clientSeq continuity bounds how many changes a pack carries, so
	// without a per-pack budget a few hundred puts at the per-put bound would
	// reproduce the fan-out that bound caps.
	t.Run("puts past the pack budget share the put of their last change", func(t *testing.T) {
		// Puts at the per-put bound, so the budget pays for exactly
		// maxPresencePutPerPack / maxPresencePutSize of them, and each is a
		// different size so the put kept at the end names which one it is.
		var pcs []*presence.Change
		paid := maxPresencePutPerPack / maxPresencePutSize
		for i := range paid + 2 {
			pcs = append(pcs, put(sizedPresence(maxPresencePutSize-i)))
		}
		pack := presencePack(1, pcs...)

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		for i := range paid {
			assert.Equal(t, pcs[i], pack.Changes[i].PresenceChange(), "change %d", i)
		}
		// The rest of the run costs one put between them, and it carries the
		// presence the sender holds.
		assert.Nil(t, pack.Changes[paid].PresenceChange())
		assert.Equal(t, pcs[paid+1], pack.Changes[paid+1].PresenceChange())
	})

	// pushPack drops it, so the fan-out these bound never happens; a client
	// resending one pushed before the bounds existed must not lose it.
	t.Run("a put at or below the checkpoint is not measured", func(t *testing.T) {
		data := sizedPresence(maxPresencePutSize + 1)
		pack := presencePack(1, put(data))

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 7, 1), docKey, pack)
		require.NoError(t, err)
		assert.Equal(t, put(data), pack.Changes[0].PresenceChange())
	})

	// A client keeps a local change until the server acknowledges it, so a
	// refused pack is pushed again unchanged -- including the pack that
	// detaches the client, which ends with a clear.
	t.Run("a detach pack carrying a put over the bound is not refused", func(t *testing.T) {
		pack := presencePack(1, put(sizedPresence(maxPresencePutSize+1)), clearChange())

		_, err := foldPresencePatches(newCacheBackend(t), clientAt(docKey, 0, 0), docKey, pack)
		require.NoError(t, err)
		assert.Nil(t, pack.Changes[0].PresenceChange())
		assert.Equal(t, clearChange(), pack.Changes[1].PresenceChange())
	})
}
