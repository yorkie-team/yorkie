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
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// ErrPresenceBaseUnavailable is returned when a pushed presence patch cannot
// be folded because the server does not hold the sender's current presence:
// the cache entry was evicted, the server restarted, or the client's previous
// push went through another server. Nothing in the pack is stored; the client
// resends the pack with the patch replaced by a full put.
var ErrPresenceBaseUnavailable = errors.FailedPrecond(
	"presence base unavailable",
).WithCode("ErrPresenceBaseUnavailable")

// maxPresenceBaseSize bounds the presence one cache entry holds, counted as
// the bytes of its keys and values. Presence is excluded from every document
// size gate, and the LRU bounds entries rather than bytes, so without this a
// client could park an arbitrarily large presence per (document, client) and
// the cache would be bounded only by entry count: at the default 10,000
// entries this keeps it near 40 MiB. It also bounds what the fold hands the
// store and the fan-out, so a small patch cannot expand into an arbitrarily
// large put.
//
// A client whose presence does not fit keeps its presence; the server only
// never folds its patches, so it pushes full puts, which is what every client
// did before patches existed.
const maxPresenceBaseSize = 4 * 1024

// presenceSize returns the bytes of presence data, keys plus values. It is the
// same measure the cache is budgeted in; the map overhead itself is ignored.
func presenceSize(data presence.Data) int {
	size := 0
	for k, v := range data {
		size += len(k) + len(v)
	}
	return size
}

// presenceFold carries the client's presence from before a push, read from
// the cache in foldPresencePatches, to commit, which writes back the presence
// after the push once it is stored and the client's checkpoint is final.
type presenceFold struct {
	key cache.PresenceBaseKey

	// base is the client's full presence before the push; nil when unknown.
	base presence.Data
}

// foldPresencePatches replaces each presence patch in the pack with a put of
// the sender's full presence, so only puts are stored and pulled. It walks the
// changes pushPack will store, in clientSeq order, from the cached presence of
// this client.
//
// A patch it has no base for is dropped when a later put or clear in the same
// pack replaces the presence anyway, which is how a detach pack ends;
// otherwise the push fails with ErrPresenceBaseUnavailable. It returns nil
// when there is nothing to commit: no cached base and nothing to push.
func foldPresencePatches(
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docKey types.DocRefKey,
	reqPack *change.Pack,
) (*presenceFold, error) {
	cp := clientInfo.Checkpoint(docKey.DocID)
	fold := &presenceFold{
		key: cache.PresenceBaseKey{DocRefKey: docKey, ClientID: clientInfo.ID},
	}
	// The initial checkpoint identifies no attachment: it is both what a
	// detach zeroes the client's checkpoint to and what a fresh attach seeds,
	// so a base matching it could belong to an earlier attachment of the same
	// client on this server. commit does not store one, and a leftover entry
	// -- written before this check, or by a push whose pull then failed -- is
	// not trusted here either.
	if cp != change.InitialCheckpoint {
		if cached, ok := be.Cache.PresenceBase.Get(fold.key); ok &&
			cached.Checkpoint == cp && cached.Epoch == clientEpoch(clientInfo, docKey.DocID) {
			fold.base = cached.Presence
		}
	}

	var pushables []*change.Change
	lastReset := -1
	for _, cn := range reqPack.Changes {
		if cn.ClientSeq() <= cp.ClientSeq {
			continue
		}
		if pc := cn.PresenceChange(); pc != nil && (pc.ChangeType == presence.Put || pc.ChangeType == presence.Clear) {
			lastReset = len(pushables)
		}
		pushables = append(pushables, cn)
	}
	if len(pushables) == 0 && fold.base == nil {
		return nil, nil
	}

	base := fold.base
	for i, cn := range pushables {
		pc := cn.PresenceChange()
		if pc == nil {
			continue
		}

		switch pc.ChangeType {
		case presence.Put:
			base = pc.Presence
		case presence.Clear:
			base = nil
		case presence.Patch:
			if base == nil {
				if i < lastReset {
					cn.SetPresenceChange(nil)
					continue
				}
				return nil, ErrPresenceBaseUnavailable
			}
			base = pc.ApplyTo(base)
			cn.SetPresenceChange(&presence.Change{
				ChangeType: presence.Put,
				Presence:   base,
			})
		}
	}

	return fold, nil
}

// commit records the client's presence after the push as the base for its
// next one, keyed to the client's checkpoint after the pull. It walks pushed,
// the changes pushPack actually stored, so a push whose changes were
// discarded (a stale epoch, the size gate on a detach) leaves the base as it
// was.
func (f *presenceFold) commit(
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docID types.ID,
	pushed []*database.ChangeInfo,
) {
	if f == nil {
		return
	}

	base, changed := f.base, false
	for _, info := range pushed {
		if info.PresenceChange == nil {
			continue
		}
		switch info.PresenceChange.ChangeType {
		case presence.Put:
			base, changed = info.PresenceChange.Presence, true
		case presence.Clear:
			base, changed = nil, true
		}
	}

	if base == nil {
		be.Cache.PresenceBase.Remove(f.key)
		return
	}

	// Hold nothing for a client that is no longer attached. DetachDocument
	// zeroes the client's checkpoint to the (0, 0) a fresh attach seeds, so a
	// base kept past a detach would pass the freshness check on the client's
	// next attachment to this server and fold its first patch onto presence
	// from the previous one. The same goes for a checkpoint that is still the
	// seeded one: it tells this attachment apart from no other.
	cp := clientInfo.Checkpoint(docID)
	if attached, err := clientInfo.IsAttached(docID); err != nil || !attached ||
		cp == change.InitialCheckpoint {
		be.Cache.PresenceBase.Remove(f.key)
		return
	}

	// Presence is excluded from every document size gate, so the cache is the
	// only place that bounds it. A presence too large to hold is dropped
	// rather than stored: the client's next patch is refused and it pushes a
	// full put, which is what it would have pushed anyway.
	if presenceSize(base) > maxPresenceBaseSize {
		be.Cache.PresenceBase.Remove(f.key)
		return
	}

	// A presence taken from a stored change is shared with it; one carried
	// over from the cache already belongs to the cache.
	if changed {
		base = base.DeepCopy()
	}
	be.Cache.PresenceBase.Add(f.key, cache.PresenceBase{
		Checkpoint: cp,
		Epoch:      clientEpoch(clientInfo, docID),
		Presence:   base,
	})
}

// clientEpoch returns the document epoch the client is attached under.
func clientEpoch(clientInfo *database.ClientInfo, docID types.ID) int64 {
	if docInfo := clientInfo.Documents[docID]; docInfo != nil {
		return docInfo.Epoch
	}
	return 0
}
