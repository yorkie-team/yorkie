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

package packs

import (
	"context"
	stderrors "errors"
	"fmt"
	"strconv"
	gotime "time"

	"connectrpc.com/connect"
	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/pkg/units"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// DocKey generates document-wide sync key.
func DocKey(projectID types.ID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("doc-%s-%s", projectID, docKey))
}

// DocPushKey generates a sync key for pushing changes to the document.
func DocPushKey(docKey types.DocRefKey) sync.Key {
	return sync.NewKey(fmt.Sprintf("doc-push-%s-%s", docKey.ProjectID, docKey.DocID))
}

// DocPullKey generates a sync key for pulling changes from the document.
func DocPullKey(clientID time.ActorID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("doc-pull-%s-%s", clientID, docKey))
}

// PushPullOptions represents the options for PushPull.
type PushPullOptions struct {
	// Mode represents the sync mode.
	Mode types.SyncMode

	// Status represents the status of the document to be updated.
	Status document.StatusType

	// DisableGC, when true, makes the server skip minVV tracking for this
	// client and omit the response VersionVector. Set per-request by the
	// RPC handler from the matching wire field. See
	// docs/design/disable-gc-on-attach.md.
	DisableGC bool

	// DisablePresence forwards the document-scope opt-out persisted on
	// DocInfo. When true, the PushPull pipeline strips presence on both
	// the write and read paths so the response carries an empty presence
	// map regardless of what any client sends.
	DisablePresence bool

	// IsAttach marks the PushPull that runs as part of AttachDocument. Only a
	// FRESH attach — one whose seeded checkpoint is still 0/0 — is exempt from
	// the already-stored filter in pushPack: it may legitimately carry local
	// edits made before the attach, whose clientSeq and lamport both start at
	// 1 — below anything the same stable actor stored during an earlier
	// attachment, and so indistinguishable from a re-send by metadata alone.
	//
	// A resumed (Case-B) attach seeds the presented checkpoint verbatim
	// (ClientInfo.AttachDocument), so its clientSeq does not restart and the
	// filter applies to it like any other sync. See isFreshAttach.
	//
	// Known limitation: a fresh attach that fails after CreateChangeInfos and
	// is retried is still exempt, so its pre-attach changes can be stored
	// twice. Attach's own duplicate guard does not close this window —
	// ClientInfo.AttachDocument only rejects with ErrDocumentAlreadyAttached
	// once the status has been persisted as DocumentAttached, while the
	// interrupted attempt leaves it at Attaching (clients.TryAttaching). See
	// docs/design/pushpull-idempotency.md.
	IsAttach bool
}

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.Internal("invalid server seq").WithCode("ErrInvalidServerSeq")

	// ErrEpochMismatch is returned when the client's epoch does not match the
	// document's epoch. This happens after compaction resets the document — the
	// client must detach and re-attach to receive the compacted state.
	ErrEpochMismatch = errors.FailedPrecond("epoch mismatch").WithCode("ErrEpochMismatch")
)

// PushPull stores the given changes and returns accumulated changes of the
// given document.
func PushPull(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientInfo *database.ClientInfo,
	docKey types.DocRefKey,
	reqPack *change.Pack,
	opts PushPullOptions,
) (*ServerPack, error) {
	start := gotime.Now()
	hostname := be.Config.Hostname

	// 00. Validate ClientSeq continuity against the original request.
	// Presence stripping must not run first: presence-only changes also
	// occupy ClientSeq, and dropping them early would hide gaps.
	if err := validateClientSeqContinuity(clientInfo.Checkpoint(docKey.DocID), reqPack); err != nil {
		be.Metrics.AddPushPullErrors(hostname, project, 1)
		return nil, err
	}

	// 01. Strip presence on the way in when the document opted out. Doing
	// this before pushPack means no presence-only change ever reaches the
	// changes collection, regardless of which SDK version sent it.
	if opts.DisablePresence {
		reqPack.Changes = stripPresenceChanges(reqPack.Changes)
	}

	// Snapshot what arrived on the wire: pushPack prunes reqPack.Changes of
	// the changes the database already holds, and the received metrics report
	// what the client sent, not what survived the filters.
	receivedChanges, receivedOperations := reqPack.ChangesLen(), reqPack.OperationsLen()

	// 02. push the change pack to the database.
	// ServerSeq checks need a DocInfo snapshot under DocPushKey and must
	// run after epoch mismatch handling, so they live in pushPack.
	pushedChanges, storedDuplicates, docInfo, initialSeq, cpAfterPush, err := pushPack(
		ctx, be, clientInfo, docKey, reqPack, opts,
	)
	if err != nil {
		be.Metrics.AddPushPullErrors(hostname, project, 1)
		return nil, err
	}

	// 03. pull the pack from the database.
	resPack, err := pullPack(ctx, be, clientInfo, project.SnapshotThreshold,
		docInfo, reqPack, cpAfterPush, initialSeq, opts)

	if err != nil {
		be.Metrics.AddPushPullErrors(hostname, project, 1)
		return nil, err
	}

	if logging.Enabled(zap.DebugLevel) {
		pullLog := strconv.Itoa(resPack.ChangesLen())
		if resPack.SnapshotLen() > 0 {
			pullLog = units.HumanSize(float64(resPack.SnapshotLen()))
		}
		logging.From(ctx).Debugf(
			"SYNC: '%s' is synced by '%s', push: %d, pull: %s, elapsed: %s",
			docInfo.Key,
			clientInfo.Key,
			len(pushedChanges),
			pullLog,
			gotime.Since(start),
		)
	}

	be.Metrics.AddPushPullReceivedChanges(hostname, project, receivedChanges)
	be.Metrics.AddPushPullReceivedOperations(hostname, project, receivedOperations)
	be.Metrics.AddPushPullSentChanges(hostname, project, resPack.ChangesLen())
	be.Metrics.AddPushPullSentOperations(hostname, project, resPack.OperationsLen())
	be.Metrics.AddPushPullSnapshotBytes(hostname, project, resPack.SnapshotLen())
	be.Metrics.ObservePushPullResponseSeconds(gotime.Since(start).Seconds())

	// 04. publish document event and store the snapshot if needed.
	// storedDuplicates counts too: those changes are durably in the database
	// but were stored by an attempt that failed before it could announce them,
	// and this retry is the only chance left to do so.
	if len(pushedChanges) > 0 || len(storedDuplicates) > 0 || reqPack.IsRemoved {
		be.Go(func(ctx context.Context) {
			// Publish under the actor of an accepted change so the pubsub
			// self-echo filter (doc_subscription.go drops events whose Actor
			// equals the subscriber) recognizes the author's own event. Watch
			// subscribes under that same actor (WatchRequest.actor_id,
			// yorkie_server.go Watch): new SDKs under the stable actor, old SDKs
			// under the session id. Read it from an accepted change
			// (pushedChanges), not reqPack.Changes[0], which may be an
			// already-acknowledged change with a different actor in a malformed
			// pack. A remove-only pack has no accepted change, so fall back to the
			// session id; the client is detaching (its Watch is torn down), so a
			// missed self-echo is moot. OwnActorID() is not used for the fallback:
			// ActivateClient sets StableActorID for every client, so it would
			// return the stable actor even for old SDKs that subscribe under the
			// session id.
			publisher, err := clientInfo.ID.ToActorID()
			if err != nil {
				logging.From(ctx).Error(err)
				return
			}
			announced := pushedChanges
			if len(announced) == 0 {
				announced = storedDuplicates
			}
			if len(announced) > 0 {
				publisher, err = announced[0].ActorID.ToActorID()
				if err != nil {
					logging.From(ctx).Error(err)
					return
				}
			}

			// TODO(hackerwins): For now, we are publishing the event to pubsub and
			// webhook manually. But we need to consider unified event handling system
			// to handle this with rate-limiter and retry mechanism.
			be.PubSub.Publish(ctx, publisher, events.DocEvent{
				Type:  events.DocChanged,
				Actor: publisher,
				Key:   docKey,
			})

			rootChanged := reqPack.OperationsLen() > 0 || len(storedDuplicates) > 0
			if rootChanged && project.RequireEventWebhook(events.DocRootChanged.WebhookType()) {
				options, err := project.GetEventWebhookOptions()
				if err != nil {
					logging.From(ctx).Error(err)
					return
				}
				if err := be.EventWebhookManager.Send(ctx, types.NewEventWebhookInfo(
					docKey,
					events.DocRootChanged.WebhookType(),
					project.SecretKey,
					project.EventWebhookURL,
					docInfo.Key.String(),
					options,
				)); err != nil {
					logging.From(ctx).Error(err)
					return
				}
			}
			if err := storeSnapshot(ctx, be, project.SnapshotInterval, docInfo); err != nil {
				logging.From(ctx).Error(err)
			}
		}, "pushpull")
	}

	return resPack, nil
}

func validateClientSeqContinuity(cpBeforePush change.Checkpoint, reqPack *change.Pack) error {
	// The clientSeq of the changes in the request pack must be continuous.
	expectedClientSeq := cpBeforePush.ClientSeq + 1
	for _, cn := range reqPack.Changes {
		if cn.ID().ClientSeq() <= cpBeforePush.ClientSeq {
			continue
		}

		if cn.ID().ClientSeq() != expectedClientSeq {
			return connect.NewError(
				connect.CodeInvalidArgument,
				errors.InvalidArgument("change clientSeq must increase by one").WithCode("ErrInvalidClientSeq"),
			)
		}

		expectedClientSeq++
	}

	return nil
}

// pushPack pushes the given ChangePack to the database. It returns the changes
// it stored and, separately, the changes it dropped because the database
// already held them: those are durable but were never announced by the attempt
// that stored them, so the caller still has to publish for them.
func pushPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docKey types.DocRefKey,
	reqPack *change.Pack,
	opts PushPullOptions,
) ([]*database.ChangeInfo, []*database.ChangeInfo, *database.DocInfo, int64, change.Checkpoint, error) {
	cpBeforePush := clientInfo.Checkpoint(docKey.DocID)
	var storedDuplicates []*database.ChangeInfo

	// 01. Filter out changes that are already pushed.
	//
	// A change the checkpoint already acknowledges is durable, and the pull
	// path replays reqPack.Changes on top of a document built through
	// initialSeq (pullSnapshot), which holds that stored copy. Drop it from the
	// request pack as well as from the write, or the snapshot branch applies it
	// a second time — the same double-application the already-stored filter
	// below avoids for its own drops.
	var pushables []*database.ChangeInfo
	var unacked []*change.Change
	received := len(reqPack.Changes)
	for _, cn := range reqPack.Changes {
		if cn.ID().ClientSeq() <= cpBeforePush.ClientSeq {
			logging.From(ctx).Warnf(
				"change already pushed, clientSeq: %d, cp: %d",
				cn.ID().ClientSeq(),
				cpBeforePush.ClientSeq,
			)
			continue
		}
		info, err := database.NewFromChange(docKey, cn)
		if err != nil {
			return nil, nil, nil, time.InitialLamport, change.InitialCheckpoint, err
		}

		unacked = append(unacked, cn)
		pushables = append(pushables, info)
	}
	reqPack.Changes = unacked

	// 02. Push the changes to the database.
	// NOTE(hackerwins): The lock must be acquired before the epoch check
	// because FindDocInfoByRefKey populates the docCache. Without the lock,
	// a concurrent goroutine's stale MongoDB read can overwrite a fresher
	// cache entry, causing ErrConflictOnUpdate in CreateChangeInfos.
	// ServerSeq validation also belongs here so it observes the same locked
	// DocInfo snapshot and never runs ahead of epoch mismatch handling.
	if len(pushables) > 0 || reqPack.IsRemoved {
		locker := be.Lockers.Locker(DocPushKey(docKey))
		defer locker.Unlock()

		currentDocInfo, err := be.DB.FindDocInfoByRefKey(ctx, docKey)
		if err != nil {
			return nil, nil, nil, time.InitialLamport, change.InitialCheckpoint, err
		}

		clientDocInfo := clientInfo.Documents[docKey.DocID]
		epochMismatch := clientDocInfo != nil && clientDocInfo.Epoch != currentDocInfo.Epoch
		if epochMismatch {
			// 03. Discard stale-epoch changes before storing them.
			// pushPack runs before preparePack. Without this check, stale-epoch
			// changes would be inserted into the in-memory changeCache, polluting
			// it with operations that reference pre-compaction CRDT node IDs.
			// preparePack will return ErrEpochMismatch to the client downstream.
			if len(pushables) > 0 {
				logging.From(ctx).Warnf(
					"discarding %d changes from stale epoch: client(%d) != doc(%d)",
					len(pushables),
					clientDocInfo.Epoch,
					currentDocInfo.Epoch,
				)
				pushables = nil
			}
		} else if reqPack.Checkpoint.ServerSeq > currentDocInfo.ServerSeq {
			return nil, nil, nil, time.InitialLamport, change.InitialCheckpoint, connect.NewError(
				connect.CodeInvalidArgument,
				errors.InvalidArgument("checkpoint serverSeq exceeds server state").WithCode("ErrInvalidServerSeq"),
			)
		} else if len(pushables) > 0 && !isFreshAttach(opts, cpBeforePush) &&
			!clientInfo.IsServerClient() && currentDocInfo.ServerSeq > cpBeforePush.ServerSeq {
			// 04. Drop changes the database already holds. A duplicate can only
			// have been stored after this client was last acknowledged, so a
			// document that has not moved since cpBeforePush rules one out
			// without a query and keeps the single-writer path free of it.
			//
			// Server-side clients are excluded: database.SystemClientInfo seeds
			// ClientSeq 0 for every call, so every system push stamps
			// InitialActorID with clientSeq 1 and a second admin update would
			// look like a re-send of the first.
			remaining, dropped, maxStoredClientSeq, err := filterStoredChanges(
				ctx, be, clientInfo, docKey, currentDocInfo.ServerSeq, pushables,
			)
			if err != nil {
				return nil, nil, nil, time.InitialLamport, change.InitialCheckpoint, err
			}

			if len(dropped) > 0 {
				// Acknowledge what was dropped. The client re-sent it because
				// the checkpoint never caught up; leaving the checkpoint behind
				// would make it re-send forever.
				pushables = remaining
				storedDuplicates = dropped
				cpBeforePush = cpBeforePush.SyncClientSeq(maxStoredClientSeq)

				// The pull path replays reqPack.Changes on top of a document
				// already built through initialSeq (pullSnapshot), which holds
				// the stored copy. A duplicate left in the request pack would
				// be applied a second time there, so drop it from the pack as
				// well as from the write.
				reqPack.Changes = dropStoredChanges(reqPack.Changes, dropped)
			}
		}
	}
	docInfo, cpAfterPush, err := be.DB.CreateChangeInfos(
		ctx,
		docKey,
		cpBeforePush,
		pushables,
		reqPack.IsRemoved,
	)
	if err != nil {
		return nil, nil, nil, time.InitialLamport, change.InitialCheckpoint, err
	}

	initialSeq := docInfo.ServerSeq - int64(len(pushables))
	if received > 0 {
		logging.From(ctx).Debugf(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.Key,
			len(pushables),
			docInfo.Key,
			received-len(pushables),
			initialSeq,
			docInfo.ServerSeq,
			cpAfterPush,
		)
	}

	return pushables, storedDuplicates, docInfo, initialSeq, cpAfterPush, nil
}

// isFreshAttach reports whether this PushPull is the attach of a client that
// has never synced this document. ClientInfo.AttachDocument seeds 0/0 for a
// fresh attach and the presented checkpoint verbatim for a resumed one, so the
// seeded checkpoint is what separates the two — not the RPC. Only the fresh
// case restarts clientSeq at 1 and is therefore exempt from the already-stored
// filter; see PushPullOptions.IsAttach.
func isFreshAttach(opts PushPullOptions, cpBeforePush change.Checkpoint) bool {
	return opts.IsAttach && cpBeforePush.ServerSeq == 0 && cpBeforePush.ClientSeq == 0
}

// storedKey identifies a change within a document: an actor and the clientSeq
// it stamped. It is only ever built from changes the server itself matched as
// already stored.
type storedKey struct {
	actorID   types.ID
	clientSeq uint32
}

// dropStoredChanges removes the already-stored changes from the given slice.
func dropStoredChanges(changes []*change.Change, dropped []*database.ChangeInfo) []*change.Change {
	stored := make(map[storedKey]struct{}, len(dropped))
	for _, info := range dropped {
		stored[storedKey{actorID: info.ActorID, clientSeq: info.ClientSeq}] = struct{}{}
	}

	var remaining []*change.Change
	for _, cn := range changes {
		key := storedKey{
			actorID:   types.ID(cn.ID().ActorID().String()),
			clientSeq: cn.ID().ClientSeq(),
		}
		if _, ok := stored[key]; ok {
			continue
		}
		remaining = append(remaining, cn)
	}

	return remaining
}

// filterStoredChanges splits the pushables into the ones the database does not
// hold yet and the ones it already holds, and returns the highest clientSeq it
// dropped alongside them.
//
// The ClientSeq checkpoint on ClientInfo cannot catch these on its own.
// PushPull is not atomic: CreateChangeInfos stores the changes and
// UpdateClientInfoAfterPushPull advances the checkpoint, so a request can
// store its changes and then fail before the checkpoint moves. The client
// never sees a response, retries the same pack, and the stale checkpoint lets
// every change through a second time. The changes collection is the
// authoritative record of what was stored, so ask it directly.
//
// A change counts as already stored when the actor's latest stored change is
// at or beyond it in BOTH clientSeq and lamport, AND the pack itself contains
// that latest stored change verbatim — same clientSeq, same lamport. Neither
// field is a dedup key alone: clientSeq restarts at 1 for a client that
// re-attaches under the same stable actor, and that client's post-attach
// lamports sit above everything the document holds because attach syncs its
// clock first. Only an actual re-send is behind on both.
//
// The anchor — the pack holding the actor's own latest stored change — is what
// makes the comparison a statement about THIS pack rather than about a
// watermark someone else set. A re-send always carries it: the attempt that
// stored the batch left its last change as the actor's latest, and the client
// re-sends from its stale checkpoint, so that change is still in the pack.
// Without the anchor, any pack whose metadata merely sits below the watermark
// would be discarded — which is reachable when two sessions share one
// StableActorID (same project, same client key), since the watermark is then
// raised by the other session and the loser's genuinely new changes would be
// dropped and acknowledged. With it, that requires the two sessions to collide
// on an exact (clientSeq, lamport) pair.
//
// Two classes of change are never eligible, because for them the comparison is
// not a statement about a re-send:
//
//   - A change whose actor is not the pushing client's own. ActorID arrives
//     verbatim from the wire (converter.FromChangePack) and nothing on the push
//     path proves a change belongs to the actor it names, so keying a silent
//     drop plus a forward checkpoint ack on a foreign actor would let one
//     client suppress another's writes. It also bounds the work done under the
//     exclusive DocPushKey lock to one query per pack rather than one per
//     distinct actor in it.
//   - A change carrying no operations. change.Context.ToChange builds those
//     via ID.Next(true), which leaves lamport at 0, and no stored lamport can
//     sit below that — `info.Lamport <= latest.Lamport` would hold for free and
//     drop presence-only changes (cluster detach's presence clear, every
//     presence update after a re-attach) that are not re-sends at all. This is
//     the documented gap: presence-only re-sends are not deduped. See
//     docs/design/pushpull-idempotency.md.
func filterStoredChanges(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docKey types.DocRefKey,
	serverSeq int64,
	pushables []*database.ChangeInfo,
) ([]*database.ChangeInfo, []*database.ChangeInfo, uint32, error) {
	latestByActor := make(map[types.ID]*database.ChangeInfo)
	anchoredByActor := make(map[types.ID]bool)
	var remaining, dropped []*database.ChangeInfo
	var maxStoredClientSeq uint32

	for _, info := range pushables {
		if !clientInfo.IsOwnActor(info.ActorID) || info.Lamport <= 0 {
			remaining = append(remaining, info)
			continue
		}

		latest, ok := latestByActor[info.ActorID]
		if !ok {
			var err error
			latest, err = be.DB.FindLatestChangeInfoByActor(ctx, docKey, info.ActorID, serverSeq)
			if err != nil && !stderrors.Is(err, database.ErrChangeNotFound) {
				return nil, nil, 0, err
			}
			latestByActor[info.ActorID] = latest
		}

		// An actor with no stored change is reported as ErrChangeNotFound by
		// one database implementation and as a zero-valued ChangeInfo by the
		// other; a stored row always names its actor. Both implementations
		// report the latest change that CARRIES OPERATIONS: Mongo never writes
		// presence-only changes to the changes collection, and the memory
		// backend skips those rows in FindLatestChangeInfoByActor so the two
		// agree. Without that a presence-only latest row — lamport 0 — would
		// silently disable this filter on the memory backend.
		if latest == nil || latest.ActorID == "" {
			remaining = append(remaining, info)
			continue
		}

		// The pack must contain the actor's latest stored change itself,
		// otherwise it is not the batch that was stored and the comparison
		// below is about someone else's watermark. Computed once per actor:
		// the answer depends only on the pack and that actor's latest row.
		anchored, ok := anchoredByActor[info.ActorID]
		if !ok {
			anchored = isAnchoredBy(pushables, latest)
			anchoredByActor[info.ActorID] = anchored
		}
		if !anchored {
			remaining = append(remaining, info)
			continue
		}

		if info.ClientSeq <= latest.ClientSeq && info.Lamport <= latest.Lamport {
			logging.From(ctx).Warnf(
				"change already stored, actor: %s, clientSeq: %d, lamport: %d",
				info.ActorID,
				info.ClientSeq,
				info.Lamport,
			)
			maxStoredClientSeq = max(maxStoredClientSeq, info.ClientSeq)
			dropped = append(dropped, info)
			continue
		}

		remaining = append(remaining, info)
	}

	return remaining, dropped, maxStoredClientSeq, nil
}

// isAnchoredBy reports whether the pack re-sends the given stored change: one
// of its pushables names the same actor and carries the same clientSeq and
// lamport. See filterStoredChanges for why a drop requires it.
func isAnchoredBy(pushables []*database.ChangeInfo, latest *database.ChangeInfo) bool {
	for _, info := range pushables {
		if info.ActorID == latest.ActorID &&
			info.ClientSeq == latest.ClientSeq &&
			info.Lamport == latest.Lamport {
			return true
		}
	}

	return false
}

func pullPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	snapshotThreshold int64,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialSeq int64,
	opts PushPullOptions,
) (*ServerPack, error) {
	// 01. pull changes or a snapshot from the database and create a response pack.
	resPack, err := preparePack(ctx, be, clientInfo, snapshotThreshold,
		docInfo, reqPack, cpAfterPush, initialSeq, opts)

	if err != nil {
		// NOTE(hackerwins): When a client detaches after a compaction, the epoch
		// mismatch makes change sync impossible. But detach only needs to update
		// the client's status — syncing old-epoch changes is meaningless. So we
		// skip the pull and return an empty pack to let the detach proceed.
		isDetachOrRemove := opts.Status == document.StatusDetached ||
			opts.Status == document.StatusRemoved
		if stderrors.Is(err, ErrEpochMismatch) && isDetachOrRemove {
			resPack = NewServerPack(docInfo.Key, change.Checkpoint{
				ServerSeq: reqPack.Checkpoint.ServerSeq,
				ClientSeq: cpAfterPush.ClientSeq,
			}, nil, nil)
		} else {
			return nil, err
		}
	}
	resPack.ApplyDocInfo(docInfo)

	// 02. update the document's status in the client.
	if err := clientInfo.UpdateDocStatus(docInfo.ID, opts.Status, resPack.Checkpoint); err != nil {
		return nil, err
	}

	// 03. update client's vector and checkpoint to DB.
	// Skip both minVV tracking and response VV when this PushPull is
	// flagged as GC-free. The client never consumes the response VV for
	// tombstone GC, and excluding it from minVV is correct because
	// tombstones do not need to be kept alive for this client. See
	// docs/design/disable-gc-on-attach.md.
	if opts.DisableGC {
		if resPack.SnapshotLen() > 0 {
			// Snapshot pulls must still carry the doc's max lamport so the
			// opt-out client's change clock catches up; otherwise its
			// subsequent local Changes are produced with ticks far behind
			// the server's actual state. Truncate the full VV that
			// pullSnapshot populated down to a single entry keyed by the
			// requesting client's own actor: this preserves lamport
			// progression via SetClocks while keeping the size-1 invariant
			// the opt-out client maintains. Key on OwnActorID
			// (StableActorID for new SDKs) so the entry matches the actor
			// the client stamps into its own changes; otherwise its
			// SetClocks would write the lamport under the wrong actor and its
			// local clock would not advance.
			actorID, err := clientInfo.OwnActorID()
			if err != nil {
				return nil, err
			}
			maxLamport := resPack.VersionVector.MaxLamport()
			resPack.VersionVector = time.VersionVector{actorID: maxLamport}
		} else {
			// Change pulls do not need a pack-level lamport hint: each
			// Change.ID carries its own lamport that the client uses
			// directly via SyncLamport.
			resPack.VersionVector = nil
		}
	} else {
		minVersionVector, err := be.DB.UpdateMinVersionVector(ctx, clientInfo, docInfo.RefKey(), reqPack.VersionVector)
		if err != nil {
			return nil, err
		}
		if resPack.SnapshotLen() == 0 {
			resPack.VersionVector = minVersionVector
		}
	}
	if !clientInfo.IsServerClient() {
		if err := be.DB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
			return nil, err
		}
	}

	return resPack, nil
}

// preparePack prepares the response pack for the given request pack.
func preparePack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	snapshotThreshold int64,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq int64,
	opts PushPullOptions,
) (*ServerPack, error) {
	// NOTE(hackerwins): If the client is push-only, it does not need to pull changes.
	// So, just return the checkpoint with server seq after pushing changes.
	if opts.Mode == types.SyncModePushOnly {
		return NewServerPack(docInfo.Key, change.Checkpoint{
			ServerSeq: reqPack.Checkpoint.ServerSeq,
			ClientSeq: cpAfterPush.ClientSeq,
		}, nil, nil), nil
	}

	// Compare epochs first: if the document has been compacted since the
	// client last synced, serverSeq values from different epochs cannot
	// be compared.
	if clientDocInfo := clientInfo.Documents[docInfo.ID]; clientDocInfo != nil && clientDocInfo.Epoch != docInfo.Epoch {
		return nil, fmt.Errorf(
			"client epoch(%d) != document epoch(%d): %w",
			clientDocInfo.Epoch,
			docInfo.Epoch,
			ErrEpochMismatch,
		)
	}

	if initialServerSeq < reqPack.Checkpoint.ServerSeq {
		return nil, errors.InvalidArgument(
			"checkpoint serverSeq exceeds server state",
		).WithCode("ErrInvalidServerSeq")
	}

	// Pull changes from DB if the size of changes for the response is less than the snapshot threshold.
	if initialServerSeq-reqPack.Checkpoint.ServerSeq < snapshotThreshold {
		cpAfterPull, pulledChanges, err := pullChangeInfos(
			ctx,
			be,
			clientInfo,
			docInfo,
			reqPack,
			cpAfterPush,
			initialServerSeq,
		)
		if err != nil {
			return nil, err
		}

		return NewServerPack(docInfo.Key, cpAfterPull, pulledChanges, nil), nil
	}

	// NOTE(hackerwins): If the size of changes for the response is greater than the snapshot threshold,
	// we pull the snapshot from DB to reduce the size of the response.
	return pullSnapshot(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialServerSeq, opts)
}

// pullSnapshot pulls the snapshot from DB.
func pullSnapshot(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq int64,
	opts PushPullOptions,
) (*ServerPack, error) {
	doc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// NOTE(hackerwins): If the client has pushed changes, we need to apply the
	// changes to the document to build the snapshot with the changes.
	if reqPack.HasChanges() {
		if err := doc.ApplyChangePack(change.NewPack(
			docInfo.Key,
			doc.Checkpoint().NextServerSeq(docInfo.ServerSeq),
			reqPack.Changes,
			nil,
			nil,
		), be.Config.SnapshotDisableGC); err != nil {
			return nil, err
		}
	}

	cpAfterPull := cpAfterPush.NextServerSeq(docInfo.ServerSeq)

	if !be.Config.SnapshotDisableGC {
		vector, err := be.DB.GetMinVersionVector(ctx, docInfo.RefKey(), doc.VersionVector())
		if err != nil {
			return nil, err
		}
		if _, err := doc.GarbageCollect(vector); err != nil {
			return nil, err
		}
	}

	// Belt-and-suspenders for presenceless documents: clear the in-memory
	// presence map so any downstream branch sees no cached entries, and
	// pass a nil presence map into SnapshotToBytes so the wire bytes carry
	// an empty map regardless of what the doc has accumulated.
	presences := doc.AllPresences()
	if opts.DisablePresence {
		doc.ResetPresences()
		presences = nil
	}

	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), presences)
	if err != nil {
		return nil, err
	}

	logging.From(ctx).Debugf(
		"PULL: '%s' build snapshot with changes(%d~%d) from '%s', cp: %s",
		clientInfo.Key,
		reqPack.Checkpoint.ServerSeq+1,
		initialServerSeq,
		docInfo.Key,
		cpAfterPull,
	)

	pack := NewServerPack(docInfo.Key, cpAfterPull, nil, snapshot)
	pack.VersionVector = doc.VersionVector()
	return pack, nil
}

func pullChangeInfos(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq int64,
) (change.Checkpoint, []*database.ChangeInfo, error) {
	pulledChanges, err := be.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.RefKey(),
		reqPack.Checkpoint.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return change.InitialCheckpoint, nil, err
	}

	// NOTE(hackerwins, humdrum): Remove changes from the pulled if the client already has them.
	// This could happen when the client has pushed changes and the server receives the changes
	// and stores them in the DB, but fails to send the response to the client.
	// And it could also happen when the client sync with push-only mode and then sync with pull mode.
	//
	// See the following test case for more details:
	//   "sync option with mixed mode test" in integration/client_test.go
	var filteredChanges []*database.ChangeInfo
	for _, pulledChange := range pulledChanges {
		// Recognize the change as this client's own via compare-both: old SDKs
		// stamp the per-session ID, new SDKs stamp the StableActorID. Both must
		// dedup, or a stable-actor client's own changes echo back and re-apply
		// (non-idempotent ops double-count).
		if clientInfo.IsOwnActor(pulledChange.ActorID) && cpAfterPush.ClientSeq >= pulledChange.ClientSeq {
			continue
		}

		// Defensive: drop any stray presence from cached or older rows on
		// the way to the wire. Strip on a clone so the cache's shared
		// pointer stays untouched, and drop the change entirely if the
		// strip leaves it carrying no operations.
		if docInfo.DisablePresence && pulledChange.PresenceChange != nil {
			pulledChange = pulledChange.DeepCopy()
			pulledChange.PresenceChange = nil
			if !pulledChange.HasOperations() {
				continue
			}
		}

		filteredChanges = append(filteredChanges, pulledChange)
	}

	cpAfterPull := cpAfterPush.NextServerSeq(docInfo.ServerSeq)

	if len(pulledChanges) > 0 {
		logging.From(ctx).Debugf(
			"PULL: '%s' pulls %d changes(%d~%d) from '%s', cp: %s, filtered changes: %d",
			clientInfo.Key,
			len(pulledChanges),
			pulledChanges[0].ServerSeq,
			pulledChanges[len(pulledChanges)-1].ServerSeq,
			docInfo.Key,
			cpAfterPull,
			len(filteredChanges),
		)
	}

	return cpAfterPull, filteredChanges, nil
}
