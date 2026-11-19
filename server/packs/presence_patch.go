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

// ErrPresenceTooLarge is returned when a pushed presence put carries more
// than maxPresencePutSize. Nothing in the pack is stored. Resending the same
// pack cannot change the outcome; the client has to hold a smaller presence.
var ErrPresenceTooLarge = errors.ResourceExhausted(
	"presence too large",
).WithCode("ErrPresenceTooLarge")

// maxPresenceBaseSize bounds the presence one cache entry holds and the
// presence a patch may be folded into, measured by presenceSize. Presence is
// excluded from every document size gate, and the LRU bounds entries rather
// than bytes, so without this a client could park an arbitrarily large
// presence per (document, client) and the cache would be bounded only by entry
// count: at the default 10,000 entries this keeps it near 40 MiB.
//
// It is also the bound on the amplification the fold allows. A patch is
// rewritten into a put of the sender's whole presence, which is then stored
// and fanned out, so a small request can cost more than its own bytes; this
// caps that cost per folded change rather than leaving it to the size of the
// presence the sender parked earlier.
//
// A client whose presence does not fit keeps its presence; the server only
// never folds its patches, so it pushes full puts, which is what every client
// did before patches existed.
const maxPresenceBaseSize = 4 * 1024

// maxPresenceFoldPerPack bounds the presence a single pack may be folded
// into: the sum, in presenceSize, of the puts the fold rewrites that pack's
// patches into. maxPresenceBaseSize bounds one such put; nothing bounds how
// many a pack carries, and a patch is far smaller than the put it becomes, so
// without this a pack of minimal patches -- only clientSeq continuity has to
// hold -- would be rewritten into one full presence per change, each stored
// and fanned out to every client of the document. At the 16 MiB the RPC
// server reads that is on the order of 10^5 puts of 4 KiB from one request.
//
// Past the budget the fold stops writing a put per patch and keeps only the
// last one of the run, so the patches it covers cost one put between them
// instead of one each. The push is not refused: unlike a missing base, which
// the client clears by sending the presence it holds as a put, passing the
// budget is a property of the pack's shape, so a refusal would be repeated on
// a resend that replaced only the patch the server stopped on -- and the
// client is told no more than that the base was unavailable. The intermediate
// presences the run drops are the transient ones: the presence path is
// latest-wins and lossy by design, and the put kept at the end of the run is
// the presence the sender holds.
//
// At 16 times the per-put bound it leaves room for a batched pack of a few
// hundred ordinary presences -- the ~250 B cursor presence patches were
// introduced for folds about 260 times within it -- while bounding a pack's
// folded output to a constant however many patches it carries. A put the
// client sends itself starts a new run, whose kept put is bounded by the
// presence that put carried, so those stay paid for in the bytes the client
// sent.
const maxPresenceFoldPerPack = 16 * maxPresenceBaseSize

// maxPresencePutSize bounds the presence one put a client sends may carry.
// The other two bounds cover only what the server itself produces: a cache
// entry and the puts a fold writes. A put the client sent is exempt from both
// because it is paid for in the bytes the client sent -- which pays for the
// write, not for the fan-out. Presence is in no document size gate
// (checkDocumentSize measures operations, and canGrow is false for a
// presence-only change), so without this the only ceiling on a stored put is
// the 16 MiB the RPC server reads, and that put is then pulled by every
// client attached to the document: one request, N times its size out.
//
// It is not maxPresenceBaseSize. A presence above that bound works today --
// it is never cached nor folded into, so the client pushes full puts, which
// is what every client did before patches existed -- and refusing it here
// would break clients that work now. At 16 times it, the same multiple the
// pack fold budget uses, this is well above what a cursor or a selection
// needs while bounding what one push costs each peer.
const maxPresencePutSize = 16 * maxPresenceBaseSize

// presenceEntryOverhead is what one presence key costs beyond the bytes of its
// key and value: two string headers in the map, the slack a Go map keeps per
// slot, and the allocation behind each string. Counting it is what bounds the
// key count, which nothing else does: a key and value of one byte each would
// otherwise count as two bytes against maxPresenceBaseSize while costing this
// much memory, so a presence of many tiny keys could hold orders of magnitude
// more memory than the bound suggests. At 64 bytes a presence holds at most
// 64 keys, well above what a presence is for.
const presenceEntryOverhead = 64

// presenceSize returns the memory one presence costs: the bytes of its keys
// and values plus presenceEntryOverhead per key. It is the measure both the
// cache entries and the folded puts are budgeted in.
func presenceSize(data presence.Data) int {
	size := 0
	for k, v := range data {
		size += presenceEntryOverhead + len(k) + len(v)
	}
	return size
}

// checkPresenceSizes refuses a push carrying a presence put over
// maxPresencePutSize, before anything in the pack is stored.
//
// It measures only the changes above the client's checkpoint, the ones
// pushPack will store and every attached client will then pull. A change at
// or below it was stored by an earlier push and is dropped here, so a client
// resending one -- after a reconnect, say -- is not refused for a presence
// the server accepted before this bound existed.
//
// Patches are not measured: the fold already refuses one whose result passes
// maxPresenceBaseSize, so no patch reaches the store as a put larger than
// that, and a patch the fold drops is stored with no presence at all.
func checkPresenceSizes(docKey types.DocRefKey, cp change.Checkpoint, reqPack *change.Pack) error {
	for _, cn := range reqPack.Changes {
		if cn.ClientSeq() <= cp.ClientSeq {
			continue
		}

		pc := cn.PresenceChange()
		if pc == nil || pc.ChangeType != presence.Put {
			continue
		}

		if size := presenceSize(pc.Presence); size > maxPresencePutSize {
			return fmt.Errorf(
				"push to %s: presence of %d bytes at client seq %d exceeds %d: %w",
				docKey, size, cn.ClientSeq(), maxPresencePutSize, ErrPresenceTooLarge,
			)
		}
	}

	return nil
}

// presenceFold carries the client's presence from before a push, read from
// the cache in foldPresencePatches, to commit, which writes back the presence
// after the push once it is stored and the client's checkpoint is stored.
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
// otherwise the push fails with ErrPresenceBaseUnavailable. Patches past
// maxPresenceFoldPerPack keep the push but share one put between them. It
// returns nil when there is nothing to commit: no cached base and nothing to
// push.
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

	base, folded := fold.base, 0
	// coalesced is the index in pushables of the last patch the pack's fold
	// budget did not pay for a put of. Such a patch loses its presence while
	// the walk keeps folding it into base, and the presence the run of them
	// produces is attached to the last one after the walk, as a single put. A
	// put or clear ends the run: it replaces the presence anyway.
	coalesced := -1
	for i, cn := range pushables {
		pc := cn.PresenceChange()
		if pc == nil {
			continue
		}

		switch pc.ChangeType {
		case presence.Put:
			base, coalesced = pc.Presence, -1
		case presence.Clear:
			base, coalesced = nil, -1
		case presence.Patch:
			// A fold whose result would not fit the per-put bound is treated
			// as having no base. The put a patch is rewritten into is stored
			// and fanned out, so folding one over the bound would let a
			// request cost arbitrarily more than its own bytes; the sender
			// instead pushes the presence it holds as a put, paying for it
			// itself.
			next, size := base, 0
			if next != nil {
				next = pc.ApplyTo(next)
				size = presenceSize(next)
			}
			if next == nil || size > maxPresenceBaseSize {
				base, coalesced = nil, -1
				if i < lastReset {
					cn.SetPresenceChange(nil)
					continue
				}
				return nil, ErrPresenceBaseUnavailable
			}

			base = next
			// Past the pack's budget the patches of a run share one put
			// instead of getting one each, so the pack's folded output is a
			// constant however many patches it carries. Refusing the push
			// here instead would not converge: the client is told only that
			// the base was unavailable, and replacing one patch with a put
			// leaves the rest of the pack past the budget, so the same pack,
			// its operations included, would be refused again.
			if coalesced >= 0 || folded+size > maxPresenceFoldPerPack {
				cn.SetPresenceChange(nil)
				coalesced = i
				continue
			}

			folded += size
			cn.SetPresenceChange(&presence.Change{
				ChangeType: presence.Put,
				Presence:   base,
			})
		}
	}

	// What the coalesced run produced, as one put on its last change. Leaving
	// the run without any put would hold the sender's peers on a presence it
	// no longer holds until its next push.
	if coalesced >= 0 {
		pushables[coalesced].SetPresenceChange(&presence.Change{
			ChangeType: presence.Put,
			Presence:   base,
		})
	}

	return fold, nil
}

// commit records the client's presence after the push as the base for its
// next one. It walks pushed, the changes pushPack actually stored, so a push
// whose changes were discarded (a stale epoch, the size gate on a detach)
// leaves the base as it was.
//
// The base is keyed to the checkpoint stored returns, the client as the store
// holds it after the pull, because that is what the next push reads its
// checkpoint from. It is not always the reply's: the store never moves a
// checkpoint back, while a push-only reply returns the server seq the client
// sent, which can be behind the client's last pull. stored is called only when
// there is a base to keep; when it fails the base is dropped, so the client's
// next patch falls back to a full put instead of finding a base under a
// checkpoint the store does not hold.
//
// pushedCP is the checkpoint this request's own push produced. The stored row
// is only trusted to describe this request while its client seq still matches:
// the pull lock is per-process, so a concurrent push by the same client on
// another server can advance the stored client seq between the pull and the
// read-back, carrying presence changes this request never saw. Keying this
// request's older presence to that newer checkpoint would let the client's
// next patch fold onto presence the store has already moved past, so the base
// is dropped instead and the client falls back to a full put.
func (f *presenceFold) commit(
	be *backend.Backend,
	stored func() (*database.ClientInfo, error),
	docID types.ID,
	pushedCP change.Checkpoint,
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

	// Presence is excluded from every document size gate, so the cache is the
	// only place that bounds it. A presence too large to hold is dropped
	// rather than stored: the client's next patch is refused and it pushes a
	// full put, which is what it would have pushed anyway.
	if presenceSize(base) > maxPresenceBaseSize {
		be.Cache.PresenceBase.Remove(f.key)
		return
	}

	clientInfo, err := stored()
	if err != nil {
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
		cp == change.InitialCheckpoint || cp.ClientSeq != pushedCP.ClientSeq {
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
