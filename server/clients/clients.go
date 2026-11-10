/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package clients provides the client related business logic.
package clients

import (
	"context"
	stderrors "errors"
	"fmt"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/messaging"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrInvalidClientKey is returned when the given Key is not valid ClientKey.
	ErrInvalidClientKey = errors.InvalidArgument("invalid client key").WithCode("ErrInvalidClientKey")

	// ErrInvalidClientID is returned when the given Key is not valid ClientID.
	ErrInvalidClientID = errors.InvalidArgument("invalid client id").WithCode("ErrInvalidClientID")

	// ErrActorMismatch is returned when a request declares an actor that is not
	// the authenticated client's own stable actor.
	ErrActorMismatch = errors.PermissionDenied(
		"actor id does not match the authenticated client",
	).WithCode("ErrActorMismatch")
)

// Activate activates the given client.
func Activate(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientKey string,
	metadata map[string]string,
) (*database.ClientInfo, error) {
	return be.DB.ActivateClient(ctx, project.ID, clientKey, metadata)
}

// Deactivate deactivates the given client.
func Deactivate(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	// NOTE(hackerwins): In cluster using ConsistentHashing, document-specific
	// requests are assigned to particular servers. ClientInfo is cached per
	// server, which can lead to a situation where server executing deactivation
	// may not have the complete attachment information in its cache.
	// To ensure all attached documents are properly detached during client
	// deactivation, we skip the cache and fetch ClientInfo directly from DB.
	info, err := FindActiveClientInfo(ctx, be, refKey, true)
	if err != nil {
		return nil, err
	}

	docIDs := make([]types.ID, 0)
	for docID, clientDocInfo := range info.Documents {
		if clientDocInfo.Status != database.DocumentAttached &&
			clientDocInfo.Status != database.DocumentAttaching {
			continue
		}
		docIDs = append(docIDs, docID)
	}

	docInfos, err := be.DB.FindDocInfosByIDs(ctx, project.ID, docIDs)
	if err != nil {
		return nil, err
	}

	actorID, err := info.ID.ToActorID()
	if err != nil {
		return nil, err
	}

	clusterClient, err := be.ClusterClient()
	if err != nil {
		return nil, err
	}

	// One broadcast covers the whole deactivation: the detaches below and the
	// DeactivateClient write all land in the same client row, and dropping the
	// row drops every one of them at once. It is deferred rather than placed
	// after the write so a detach that already committed is still announced
	// when a later step fails — the row MongoDB holds has moved on either way,
	// and the peers' copies must not outlive it.
	defer InvalidateCachedClient(ctx, be, refKey)

	if len(docInfos) != len(docIDs) {
		present, err := detachMissingDocuments(ctx, be, info, docIDs, docInfos)
		if err != nil {
			return nil, err
		}
		docInfos = append(docInfos, present...)
	}

	for _, info := range docInfos {
		if err := clusterClient.DetachDocument(
			ctx,
			project,
			actorID,
			info.ID,
			info.Key,
		); err != nil {
			return nil, err
		}
	}

	deactivated, err := be.DB.DeactivateClient(ctx, refKey)
	if err != nil {
		return nil, err
	}

	return deactivated, nil
}

// detachMissingDocuments marks detached, in the client row, every document in
// docIDs whose row is really gone, and returns the ones the batch read left
// out that a point read does find, for the caller to detach through the
// cluster with the rest.
//
// A row that is gone was purged or deleted out of band while the client still
// held the document. It cannot be detached through the cluster any more —
// clusterServer.DetachDocument reads the DocInfo first and fails without it —
// and an entry left attached or attaching makes DeactivateClient refuse the
// client, on every housekeeping cycle, for good (#2343). The write is the one
// a detach makes to the client row.
//
// Absence from FindDocInfosByIDs is not taken as proof on its own. That read
// is one batch query, and taking a document it happened to leave out for a
// deleted one would cost the document the work only the cluster detach does:
// the presence-Clear change for the departing actor and the RemoveOnDetach
// flip. So each candidate is confirmed gone with a point read, and one the
// point read finds goes back to the normal detach path untouched.
func detachMissingDocuments(
	ctx context.Context,
	be *backend.Backend,
	info *database.ClientInfo,
	docIDs []types.ID,
	found []*database.DocInfo,
) ([]*database.DocInfo, error) {
	exists := make(map[types.ID]bool, len(found))
	for _, docInfo := range found {
		exists[docInfo.ID] = true
	}

	var missing []types.ID
	var present []*database.DocInfo
	for _, docID := range docIDs {
		if exists[docID] {
			continue
		}

		docInfo, err := be.DB.FindDocInfoByRefKey(ctx, types.DocRefKey{
			ProjectID: info.ProjectID,
			DocID:     docID,
		})
		if err == nil {
			present = append(present, docInfo)
			continue
		}
		if !stderrors.Is(err, database.ErrDocumentNotFound) {
			return nil, err
		}

		missing = append(missing, docID)
	}

	if len(missing) == 0 {
		return present, nil
	}

	// Drop every node's cached copy of the client row, this node's included,
	// before the writes below. UpdateClientInfoAfterPushPull skips its write
	// when the cached row already carries the entry it would write, and a copy
	// cached before the client attached this document again says exactly that:
	// detached. The deactivation would then look to have marked the entry while
	// the stored row still held it attached, and DeactivateClient would refuse
	// the client all the same.
	InvalidateCachedClient(ctx, be, info.RefKey())

	for _, docID := range missing {
		if err := info.DetachDocument(docID); err != nil {
			return nil, err
		}
		if err := be.DB.UpdateClientInfoAfterPushPull(ctx, info, &database.DocInfo{
			ID:        docID,
			ProjectID: info.ProjectID,
		}); err != nil {
			return nil, fmt.Errorf("detach missing document %s: %w", docID, err)
		}

		logging.From(ctx).Warnf(
			"detached document %s from client %s: the document no longer exists",
			docID, info.ID,
		)
	}

	// Read the row back from the database. The invalidation above is best
	// effort — it travels over the cluster and its failure is only logged — so
	// a skipped write is still possible, and it has to be loud here rather than
	// read as a detach that landed: the deactivation that follows would fail on
	// the entry it left behind, with nothing naming why.
	stored, err := be.DB.FindClientInfoByRefKey(ctx, info.RefKey(), true)
	if err != nil {
		return nil, err
	}
	for _, docID := range missing {
		if err := stored.EnsureDocumentAttachedOrAttaching(docID); err == nil {
			return nil, fmt.Errorf(
				"detach missing document %s: the stored client row still holds it",
				docID,
			)
		}
	}

	return present, nil
}

// InvalidateCachedClient asks every cluster node to drop its cached copy of
// the given client's row.
//
// A client row is cached per node, and nothing but a write performed on that
// same node replaces the entry: deactivation is not document-routed
// (housekeeping runs on the leader, DeactivateClient is served wherever the
// request lands), and a detach is routed only to the document's owner node.
// Without this broadcast the gates that read the row — EnsureActivated here,
// EnsureDocumentAttached in the RPC layer — keep admitting a client revoked
// elsewhere until size eviction happens to drop the entry, which is to say
// with no time bound at all. Dropping, rather than refilling, is what keeps
// this safe outside the client's cache lock: it can never put an older copy
// of the row on top of a newer one, and the next read re-reads MongoDB.
//
// Failure is logged, not returned: the write itself has already landed in
// MongoDB, and the caller must not be told it did not.
func InvalidateCachedClient(ctx context.Context, be *backend.Backend, refKey types.ClientRefKey) {
	if err := be.BroadcastCacheInvalidation(ctx, types.CacheTypeClient, refKey.CacheKey()); err != nil {
		logging.From(ctx).Warnf("broadcast client cache invalidation: %v", err)
	}
}

// DeactivateAsync deactivates the given client asynchronously.
// This function is designed to handle browser window close scenarios
// where the original context might be cancelled. It creates a new
// background context to ensure the deactivation process completes
// even if the original request context is cancelled.
func DeactivateAsync(
	be *backend.Backend,
	project *types.Project,
	refKey types.ClientRefKey,
) error {
	be.Go(func(ctx context.Context) {
		if _, err := Deactivate(ctx, be, project, refKey); err != nil {
			logging.LogError(ctx, fmt.Errorf("deactivate client asynchronously: %w", err))
		}

		if err := be.MsgBroker.Produce(
			ctx,
			messaging.ClientEventMessage{
				ProjectID: project.ID.String(),
				ClientID:  refKey.ClientID.String(),
				Timestamp: time.Now(),
				EventType: events.ClientDeactivatedEvent,
			},
		); err != nil {
			logging.LogError(ctx, fmt.Errorf("produce client deactivation event: %w", err))
		}

	}, "deactivation")

	return nil
}

// AttachDocument attaches the given document to the client.
//
// The presented checkpoint is the client-supplied pack.Checkpoint. A non-zero
// presented checkpoint is the resume signal for offline-persistent clients: it
// seeds ClientDocInfo so restored un-pushed changes push cleanly (Case B in
// docs/design/offline-resumable-attach.md). presentedEpoch is the client's
// persisted compaction epoch (from the attach ChangePack); on a Case B resume
// it is seeded so a stale epoch fires ErrEpochMismatch and re-anchors from a
// snapshot (pull-before-trust, Q2).
func AttachDocument(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	isAttached bool,
	presentedEpoch int64,
	presented change.Checkpoint,
) (*database.ClientInfo, error) {
	// NOTE(kokodak): Reattaching a document that has been detached is not allowed.
	// This check is necessary because TryAttaching does not validate this case.
	// If reattaching is allowed in the future, this IsAlreadyDetached check should be removed.
	// For more details on reattachment context, see:
	// https://github.com/yorkie-team/yorkie-js-sdk/pull/996#issuecomment-3023329082
	if clientInfo.IsAlreadyDetached(docInfo.ID, isAttached) {
		return nil, fmt.Errorf("client(%s) attaches %s: %w",
			clientInfo.ID, docInfo.ID, database.ErrDocumentAlreadyDetached)
	}

	// serverSeq safety clamp (Q2 pull-before-trust, same-epoch tier): a client
	// must not skip changes by over-claiming ServerSeq. Clamp a presented
	// ServerSeq above the doc's actual server_seq to the doc's actual, so the
	// client re-pulls anything it has not really seen. Continuity of clientSeq is
	// still guarded loudly by validateClientSeqContinuity in pushpull.
	//
	// The compaction/epoch tier is handled separately: AttachDocument seeds the
	// client's presented epoch, so a doc force-compacted while the client was
	// offline makes the seeded epoch differ from the current doc epoch, and the
	// epoch check in pushpull fires ErrEpochMismatch, re-anchoring from a snapshot.
	// See docs/design/offline-resumable-attach.md, "Pull-before-trust
	// reconciliation (Q2)".
	if presented.ServerSeq > docInfo.ServerSeq {
		presented = change.NewCheckpoint(docInfo.ServerSeq, presented.ClientSeq)
	}

	if !clientInfo.IsAttaching(docInfo.ID) {
		var err error
		clientInfo, err = be.DB.TryAttaching(ctx, clientInfo.RefKey(), docInfo.ID)
		if err != nil {
			return nil, err
		}
	}

	if err := clientInfo.AttachDocument(docInfo.ID, isAttached, docInfo.Epoch, presentedEpoch, presented); err != nil {
		return nil, err
	}

	return clientInfo, nil
}

// FindActiveClientInfo find the active client info by the given ref key.
func FindActiveClientInfo(
	ctx context.Context,
	be *backend.Backend,
	refKey types.ClientRefKey,
	skipCache ...bool,
) (*database.ClientInfo, error) {
	info, err := be.DB.FindClientInfoByRefKey(ctx, refKey, skipCache...)
	if err != nil {
		return nil, err
	}

	if err := info.EnsureActivated(); err != nil {
		return nil, err
	}

	return info, nil
}
