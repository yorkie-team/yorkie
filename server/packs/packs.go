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
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
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
	mode types.SyncMode,
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
	be.Metrics.AddPushPullReceivedChanges(reqPack.ChangesLen())
	be.Metrics.AddPushPullReceivedOperations(reqPack.OperationsLen())

	// 02. pull pack: pull changes or a snapshot from the database and create a response pack.
	respPack, err := pullPack(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialServerSeq, mode)
	if err != nil {
		return nil, err
	}
	be.Metrics.AddPushPullSentChanges(respPack.ChangesLen())
	be.Metrics.AddPushPullSentOperations(respPack.OperationsLen())
	be.Metrics.AddPushPullSnapshotBytes(respPack.SnapshotLen())

	if err := clientInfo.UpdateCheckpoint(docInfo.ID, respPack.Checkpoint); err != nil {
		return nil, err
	}

	// 03. store pushed changes, docInfo and checkpoint of the client to DB.
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

	// 04. update and find min synced ticket for garbage collection.
	// NOTE(hackerwins): Since the client could not receive the response, the
	// requested seq(reqPack) is stored instead of the response seq(resPack).
	minSyncedTicket, err := be.DB.UpdateAndFindMinSyncedTicket(
		ctx,
		clientInfo,
		docInfo.ID,
		reqPack.Checkpoint.ServerSeq,
	)
	if err != nil {
		return nil, err
	}
	respPack.MinSyncedTicket = minSyncedTicket
	respPack.ApplyDocInfo(docInfo)

	// 05. publish document change event then store snapshot asynchronously.
	if len(pushedChanges) > 0 || reqPack.IsRemoved {
		be.Background.AttachGoroutine(func(ctx context.Context) {
			publisherID, err := clientInfo.ID.ToActorID()
			if err != nil {
				logging.From(ctx).Error(err)
				return
			}

			be.Coordinator.Publish(
				ctx,
				publisherID,
				sync.DocEvent{
					Type:       types.DocumentChangedEvent,
					Publisher:  publisherID,
					DocumentID: docInfo.ID,
				},
			)

			locker, err := be.Coordinator.NewLocker(ctx, SnapshotKey(project.ID, reqPack.DocumentKey))
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
				minSyncedTicket,
			); err != nil {
				logging.From(ctx).Error(err)
			}
			be.Metrics.ObservePushPullSnapshotDurationSeconds(
				gotime.Since(start).Seconds(),
			)
		})
	}

	return respPack, nil
}

// BuildDocumentForServerSeq returns a new document for the given serverSeq.
func BuildDocumentForServerSeq(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	serverSeq int64,
) (*document.InternalDocument, error) {
	snapshotInfo, err := be.DB.FindClosestSnapshotInfo(ctx, docInfo.ID, serverSeq, true)
	if err != nil {
		return nil, err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docInfo.Key,
		snapshotInfo.ServerSeq,
		snapshotInfo.Lamport,
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
		docInfo.ID,
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
	)); err != nil {
		return nil, err
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			"after apply %d changes: elements: %d removeds: %d, %s",
			len(changes),
			doc.Root().ElementMapLen(),
			doc.Root().RemovedElementLen(),
			doc.RootObject().Marshal(),
		)
	}

	return doc, nil
}
