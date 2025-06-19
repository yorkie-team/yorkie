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
	"errors"
	"fmt"
	"strconv"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
}

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.New("invalid server seq")
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

	// 01. push the change pack to the database.
	pushedChanges, docInfo, initialSeq, cpAfterPush, err := pushPack(ctx, be, clientInfo, docKey, reqPack)
	if err != nil {
		return nil, err
	}

	// 02. pull the pack from the database.
	resPack, err := pullPack(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialSeq, opts)
	if err != nil {
		return nil, err
	}

	pullLog := strconv.Itoa(resPack.ChangesLen())
	if resPack.SnapshotLen() > 0 {
		pullLog = units.HumanSize(float64(resPack.SnapshotLen()))
	}
	logging.From(ctx).Infof(
		"SYNC: '%s' is synced by '%s', push: %d, pull: %s, elapsed: %s",
		docInfo.Key,
		clientInfo.Key,
		len(pushedChanges),
		pullLog,
		gotime.Since(start),
	)
	hostname := be.Config.Hostname
	be.Metrics.AddPushPullReceivedChanges(hostname, project, reqPack.ChangesLen())
	be.Metrics.AddPushPullReceivedOperations(hostname, project, reqPack.OperationsLen())
	be.Metrics.AddPushPullSentChanges(hostname, project, resPack.ChangesLen())
	be.Metrics.AddPushPullSentOperations(hostname, project, resPack.OperationsLen())
	be.Metrics.AddPushPullSnapshotBytes(hostname, project, resPack.SnapshotLen())
	be.Metrics.ObservePushPullResponseSeconds(gotime.Since(start).Seconds())

	// 03. publish document event and store the snapshot if needed.
	if len(pushedChanges) > 0 || reqPack.IsRemoved {
		be.Background.AttachGoroutine(func(ctx context.Context) {
			publisher, err := clientInfo.ID.ToActorID()
			if err != nil {
				logging.From(ctx).Error(err)
				return
			}

			// TODO(hackerwins): For now, we are publishing the event to pubsub and
			// webhook manually. But we need to consider unified event handling system
			// to handle this with rate-limiter and retry mechanism.
			be.PubSub.Publish(ctx, publisher, events.DocEvent{
				Type:      events.DocChanged,
				Publisher: publisher,
				DocRefKey: docKey,
			})

			if reqPack.OperationsLen() > 0 && project.RequireEventWebhook(events.DocRootChanged.WebhookType()) {
				if err := be.EventWebhookManager.Send(ctx, types.NewEventWebhookInfo(
					docKey,
					events.DocRootChanged.WebhookType(),
					project.SecretKey,
					project.EventWebhookURL,
					docInfo.Key.String(),
				)); err != nil {
					logging.From(ctx).Error(err)
					return
				}
			}

			if err := storeSnapshot(ctx, be, docInfo); err != nil {
				logging.From(ctx).Error(err)
			}
		}, "pushpull")
	}

	return resPack, nil
}

// pushPack pushes the given ChangePack to the database.
func pushPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docKey types.DocRefKey,
	reqPack *change.Pack,
) ([]*change.Change, *database.DocInfo, int64, change.Checkpoint, error) {
	cpBeforePush := clientInfo.Checkpoint(docKey.DocID)

	// 01. Filter out changes that are already pushed.
	var pushables []*change.Change
	for _, change := range reqPack.Changes {
		if change.ID().ClientSeq() <= cpBeforePush.ClientSeq {
			logging.From(ctx).Warnf(
				"change already pushed, clientSeq: %d, cp: %d",
				change.ID().ClientSeq(),
				cpBeforePush.ClientSeq,
			)
			continue
		}
		pushables = append(pushables, change)
	}

	// 02. Push the changes to the database.
	if len(pushables) > 0 || reqPack.IsRemoved {
		locker := be.Lockers.Locker(DocPushKey(docKey))
		defer locker.Unlock()
	}
	docInfo, cpAfterPush, err := be.DB.CreateChangeInfos(
		ctx,
		docKey,
		cpBeforePush,
		pushables,
		reqPack.IsRemoved,
	)
	if err != nil {
		return nil, nil, time.InitialLamport, change.InitialCheckpoint, err
	}

	initialSeq := docInfo.ServerSeq - int64(len(pushables))
	if len(reqPack.Changes) > 0 {
		logging.From(ctx).Debugf(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.Key,
			len(pushables),
			docInfo.Key,
			len(reqPack.Changes)-len(pushables),
			initialSeq,
			docInfo.ServerSeq,
			cpAfterPush,
		)
	}

	return pushables, docInfo, initialSeq, cpAfterPush, nil
}

func pullPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialSeq int64,
	opts PushPullOptions,
) (*ServerPack, error) {
	docKey := docInfo.RefKey()

	// 01. pull changes or a snapshot from the database and create a response pack.
	resPack, err := preparePack(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialSeq, opts.Mode)
	if err != nil {
		return nil, err
	}
	resPack.ApplyDocInfo(docInfo)

	// 02. update the document's status in the client.
	if opts.Status == document.StatusRemoved {
		if err := clientInfo.RemoveDocument(docInfo.ID); err != nil {
			return nil, err
		}
	} else if opts.Status == document.StatusDetached {
		if err := clientInfo.DetachDocument(docInfo.ID); err != nil {
			return nil, err
		}
	} else {
		if err := clientInfo.UpdateCheckpoint(docKey.DocID, resPack.Checkpoint); err != nil {
			return nil, err
		}
	}

	// 03. update client's vector and checkpoint to DB.
	minVersionVector, err := be.DB.UpdateMinVersionVector(ctx, clientInfo, docKey, reqPack.VersionVector)
	if err != nil {
		return nil, err
	}
	if resPack.SnapshotLen() == 0 {
		resPack.VersionVector = minVersionVector
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
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq int64,
	mode types.SyncMode,
) (*ServerPack, error) {
	// NOTE(hackerwins): If the client is push-only, it does not need to pull changes.
	// So, just return the checkpoint with server seq after pushing changes.
	if mode == types.SyncModePushOnly {
		return NewServerPack(docInfo.Key, change.Checkpoint{
			ServerSeq: reqPack.Checkpoint.ServerSeq,
			ClientSeq: cpAfterPush.ClientSeq,
		}, nil, nil), nil
	}

	if initialServerSeq < reqPack.Checkpoint.ServerSeq {
		return nil, fmt.Errorf(
			"serverSeq(%d) of request greater than serverSeq(%d) of ClientInfo: %w",
			reqPack.Checkpoint.ServerSeq,
			initialServerSeq,
			ErrInvalidServerSeq,
		)
	}

	// Pull changes from DB if the size of changes for the response is less than the snapshot threshold.
	if initialServerSeq-reqPack.Checkpoint.ServerSeq < be.Config.SnapshotThreshold {
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
	return pullSnapshot(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialServerSeq)
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

	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
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
		if clientInfo.ID == pulledChange.ActorID && cpAfterPush.ClientSeq >= pulledChange.ClientSeq {
			continue
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
