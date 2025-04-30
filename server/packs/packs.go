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

// Package packs implements PushPullPack which is used to sync the document
// between the client and the server.
package packs

import (
	"context"
	"fmt"
	"strconv"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/pkg/units"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// PushPullKey creates a new sync.Key of PushPull for the given document.
func PushPullKey(projectID types.ID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("pushpull-%s-%s", projectID, docKey))
}

// SnapshotKey creates a new sync.Key of Snapshot for the given document.
func SnapshotKey(projectID types.ID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("snapshot-%s-%s", projectID, docKey))
}

// PushPullOptions represents the options for PushPull.
type PushPullOptions struct {
	// Mode represents the sync mode.
	Mode types.SyncMode

	// Status represents the status of the document to be updated.
	Status document.StatusType
}

// PushPull stores the given changes and returns accumulated changes of the
// given document.
//
// CAUTION(hackerwins, krapie): docInfo's state is constantly mutating as they are
// constantly used as parameters in subsequent subroutines.
func PushPull(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	opts PushPullOptions,
) (*ServerPack, error) {
	start := gotime.Now()
	defer func() {
		be.Metrics.ObservePushPullResponseSeconds(gotime.Since(start).Seconds())
	}()

	// TODO: Changes may be reordered or missing during communication on the network.
	// We should check the change.pack with checkpoint to make sure the changes are in the correct order.
	initialServerSeq := docInfo.ServerSeq

	// 01. push changes: filter out the changes that are already saved in the database.
	cpAfterPush, pushedChanges := pushChanges(ctx, clientInfo, docInfo, reqPack, initialServerSeq)
	hostname := be.Config.Hostname
	be.Metrics.AddPushPullReceivedChanges(hostname, project, reqPack.ChangesLen())
	be.Metrics.AddPushPullReceivedOperations(hostname, project, reqPack.OperationsLen())

	// 02. pull pack: pull changes or a snapshot from the database and create a response pack.
	respPack, err := pullPack(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialServerSeq, opts.Mode)
	if err != nil {
		return nil, err
	}
	be.Metrics.AddPushPullSentChanges(hostname, project, respPack.ChangesLen())
	be.Metrics.AddPushPullSentOperations(hostname, project, respPack.OperationsLen())
	be.Metrics.AddPushPullSnapshotBytes(hostname, project, respPack.SnapshotLen())

	// 03. update the client's document and checkpoint.
	docRefKey := docInfo.RefKey()
	if opts.Status == document.StatusRemoved {
		if err := clientInfo.RemoveDocument(docInfo.ID); err != nil {
			return nil, err
		}
	} else if opts.Status == document.StatusDetached {
		if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
			return nil, err
		}
	} else {
		if err := clientInfo.UpdateCheckpoint(docRefKey.DocID, respPack.Checkpoint); err != nil {
			return nil, err
		}
	}

	// 04. store pushed changes, docInfo and checkpoint of the client to DB.
	if len(pushedChanges) > 0 || reqPack.IsRemoved {
		if err := be.DB.CreateChangeInfos(
			ctx,
			project.ID,
			docInfo,
			initialServerSeq,
			pushedChanges,
			reqPack.IsRemoved,
		); err != nil {
			return nil, err
		}
	}

	if err := be.DB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
		return nil, err
	}

	// 05. update and find min synced version vector for garbage collection.
	// NOTE(hackerwins): Since the client could not receive the response, the
	// requested seq(reqPack) is stored instead of the response seq(resPack).
	minSyncedVersionVector, err := be.DB.UpdateAndFindMinSyncedVersionVector(
		ctx,
		clientInfo,
		docRefKey,
		reqPack.VersionVector,
	)
	if err != nil {
		return nil, err
	}
	if respPack.SnapshotLen() == 0 {
		respPack.VersionVector = minSyncedVersionVector
	}

	// TODO(hackerwins): This is a previous implementation before the version
	// vector was introduced. But it is necessary to support the previous
	// SDKs that do not support the version vector. This code should be removed
	// after all SDKs are updated.
	respPack.MinSyncedTicket = time.InitialTicket

	respPack.ApplyDocInfo(docInfo)

	pullLog := strconv.Itoa(respPack.ChangesLen())
	if respPack.SnapshotLen() > 0 {
		pullLog = units.HumanSize(float64(respPack.SnapshotLen()))
	}
	logging.From(ctx).Infof(
		"SYNC: '%s' is synced by '%s', push: %d, pull: %s, elapsed: %s",
		docInfo.Key,
		clientInfo.Key,
		len(pushedChanges),
		pullLog,
		gotime.Since(start),
	)

	// 06. publish document change event then store snapshot asynchronously.
	if len(pushedChanges) > 0 || reqPack.IsRemoved {
		be.Background.AttachGoroutine(func(ctx context.Context) {
			publisherID, err := clientInfo.ID.ToActorID()
			if err != nil {
				logging.From(ctx).Error(err)
				return
			}

			// TODO(hackerwins): For now, we are publishing the event to pubsub and
			// webhook manually. But we need to consider unified event handling system
			// to handle this with rate-limiter and retry mechanism.
			be.PubSub.Publish(
				ctx,
				publisherID,
				events.DocEvent{
					Type:      events.DocChangedEvent,
					Publisher: publisherID,
					DocRefKey: docRefKey,
				},
			)

			if reqPack.OperationsLen() > 0 && project.RequireEventWebhook(events.DocRootChangedEvent.WebhookType()) {
				info := types.NewEventWebhookInfo(
					docRefKey,
					events.DocRootChangedEvent.WebhookType(),
					project.SecretKey,
					project.EventWebhookURL,
					docInfo.Key.String(),
				)
				if err := be.EventWebhookManager.Send(ctx, info); err != nil {
					logging.From(ctx).Error(err)
					return
				}
			}

			locker, err := be.Locker.NewLocker(ctx, SnapshotKey(project.ID, reqPack.DocumentKey))
			if err != nil {
				logging.From(ctx).Error(err)
				return
			}

			// NOTE: If the snapshot is already being created by another routine, it
			//       is not necessary to recreate it, so we can skip it.
			if err := locker.TryLock(ctx); err != nil {
				return
			}
			defer func() {
				if err := locker.Unlock(ctx); err != nil {
					logging.From(ctx).Error(err)
					return
				}
			}()

			start := gotime.Now()
			if err := storeSnapshot(
				ctx,
				be,
				docInfo,
				minSyncedVersionVector,
			); err != nil {
				logging.From(ctx).Error(err)
			}
			be.Metrics.ObservePushPullSnapshotDurationSeconds(
				gotime.Since(start).Seconds(),
			)
		}, "pushpull")
	}

	return respPack, nil
}

// BuildDocForCheckpoint returns a new document for the given checkpoint.
func BuildDocForCheckpoint(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	cp change.Checkpoint,
	actorID time.ActorID,
) (*document.Document, error) {
	internalDoc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, cp.ServerSeq)
	if err != nil {
		return nil, err
	}

	internalDoc.SetActor(actorID)
	internalDoc.SyncCheckpoint(cp.ServerSeq, cp.ClientSeq)
	return internalDoc.ToDocument(), nil
}

// BuildInternalDocForServerSeq returns a new document for the given serverSeq.
func BuildInternalDocForServerSeq(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	serverSeq int64,
) (*document.InternalDocument, error) {
	docRefKey := docInfo.RefKey()
	snapshotInfo, err := be.DB.FindClosestSnapshotInfo(
		ctx,
		docRefKey,
		serverSeq,
		true,
	)
	if err != nil {
		return nil, err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docInfo.Key,
		snapshotInfo.ServerSeq,
		snapshotInfo.Lamport,
		snapshotInfo.VersionVector,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return nil, err
	}

	// TODO(hackerwins): If the Snapshot is missing, we may have a very large
	// number of changes to read at once here. We need to split changes by a
	// certain size (e.g. 100) and read and gradually reflect it into the document.
	changes, err := be.DB.FindChangesBetweenServerSeqs(
		ctx,
		docRefKey,
		snapshotInfo.ServerSeq+1,
		serverSeq,
	)
	if err != nil {
		return nil, err
	}

	if err := doc.ApplyChangePack(change.NewPack(
		docInfo.Key,
		change.InitialCheckpoint.NextServerSeq(serverSeq),
		changes,
		nil,
		nil,
	), be.Config.SnapshotDisableGC); err != nil {
		return nil, err
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			"after apply %d changes: elements: %d removeds: %d, %s",
			len(changes),
			doc.Root().ElementMapLen(),
			doc.Root().GarbageElementLen(),
			doc.RootObject().Marshal(),
		)
	}

	return doc, nil
}

// Compact compacts the given document and its metadata and stores them in the
// database.
func Compact(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	docInfo *database.DocInfo,
) error {
	// 1. Check if the document is attached.
	isAttached, err := be.DB.IsDocumentAttached(ctx, types.DocRefKey{
		ProjectID: projectID,
		DocID:     docInfo.ID,
	}, "")
	if err != nil {
		return err
	}
	if isAttached {
		return fmt.Errorf("document is attached")
	}

	// 2. Build compacted changes and check if the content is the same.
	doc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		logging.DefaultLogger().Errorf("[CD] Document %s failed to apply changes: %v\n", docInfo.ID, err)
		return err
	}

	root, err := yson.FromCRDT(doc.RootObject())
	if err != nil {
		return err
	}

	newDoc := document.New(docInfo.Key)
	if err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetYSON(root)
		return nil
	}); err != nil {
		return err
	}

	newRoot, err := yson.FromCRDT(newDoc.RootObject())
	if err != nil {
		return err
	}

	// 3. Check if the content is the same after rebuilding.
	prevMarshalled, err := root.(yson.Object).Marshal()
	if err != nil {
		return err
	}
	newMarshalled, err := newRoot.(yson.Object).Marshal()
	if err != nil {
		return err
	}
	if prevMarshalled != newMarshalled {
		return fmt.Errorf("content mismatch after rebuild: %s", docInfo.ID)
	}

	// 4. Store compacted changes and metadata in the database.
	if err = be.DB.CompactChangeInfos(
		ctx,
		projectID,
		docInfo,
		docInfo.ServerSeq,
		newDoc.CreateChangePack().Changes,
	); err != nil {
		logging.DefaultLogger().Errorf(
			"[CD] Document %s failed to compact: %v\n Root: %s\n",
			docInfo.ID, err, prevMarshalled,
		)
		return err
	}

	return nil
}
