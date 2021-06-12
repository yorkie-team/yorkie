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

package packs

import (
	"context"
	goerrors "errors"
	"fmt"
	gotime "time"

	"github.com/pkg/errors"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = goerrors.New("invalid server seq")
)

// NewPushPullKey creates a new sync.Key of PushPull for the given document.
func NewPushPullKey(documentKey *key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("pushpull-%s", documentKey.BSONKey()))
}

// NewSnapshotKey creates a new sync.Key of Snapshot for the given document.
func NewSnapshotKey(documentKey *key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("snapshot-%s", documentKey.BSONKey()))
}

// PushPull stores the given changes and returns accumulated changes of the
// given document.
func PushPull(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	reqPack *change.Pack,
) (*change.Pack, error) {
	// TODO: Changes may be reordered or missing during communication on the network.
	// We should check the change.pack with checkpoint to make sure the changes are in the correct order.
	initialServerSeq := docInfo.ServerSeq

	// 01. push changes.
	pushedCP, pushedChanges, err := pushChanges(clientInfo, docInfo, reqPack, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// 02. pull change pack.
	respPack, err := pullPack(ctx, be, clientInfo, docInfo, reqPack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.UpdateCheckpoint(docInfo.ID, respPack.Checkpoint); err != nil {
		return nil, err
	}

	// 03. store pushed changes, document info and checkpoint of the client to DB.
	if len(pushedChanges) > 0 {
		if err := be.DB.StoreChangeInfos(ctx, docInfo, initialServerSeq, pushedChanges); err != nil {
			return nil, err
		}
	}

	if err := be.DB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
		return nil, err
	}

	// 04. update and find min synced ticket for garbage collection.
	// NOTE Since the client could not receive the PushPull response,
	//      the requested seq(reqPack) is stored instead of the response seq(resPack).
	respPack.MinSyncedTicket, err = be.DB.UpdateAndFindMinSyncedTicket(
		ctx,
		clientInfo,
		docInfo.ID,
		reqPack.Checkpoint.ServerSeq,
	)
	if err != nil {
		return nil, err
	}

	// 05. publish document change event then store snapshot asynchronously.
	if reqPack.HasChanges() {
		be.AttachGoroutine(func() {
			publisherID, err := time.ActorIDFromHex(clientInfo.ID.String())
			if err != nil {
				log.Logger.Errorf("%+v", err)
				return
			}

			ctx := context.Background()
			// TODO(hackerwins): We need to replace Lock with TryLock.
			// If the snapshot is already being created by another routine, it
			// is not necessary to recreate it, so we can skip it.
			locker, err := be.Coordinator.NewLocker(
				ctx,
				NewSnapshotKey(reqPack.DocumentKey),
			)
			if err != nil {
				log.Logger.Errorf("%+v", err)
				return
			}
			if err := locker.Lock(ctx); err != nil {
				log.Logger.Errorf("%+v", err)
				return
			}

			defer func() {
				if err := locker.Unlock(ctx); err != nil {
					log.Logger.Errorf("%+v", err)
					return
				}
			}()

			be.Coordinator.Publish(
				ctx,
				publisherID,
				sync.DocEvent{
					Type:         types.DocumentsChangedEvent,
					Publisher:    types.Client{ID: publisherID},
					DocumentKeys: []*key.Key{reqPack.DocumentKey},
				},
			)

			if err := storeSnapshot(
				ctx,
				be,
				docInfo,
			); err != nil {
				log.Logger.Errorf("%+v", err)
			}
		})
	}

	return respPack, nil
}

// pushChanges returns the changes excluding already saved in DB.
func pushChanges(
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	pack *change.Pack,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	cp := clientInfo.Checkpoint(docInfo.ID)

	var pushedChanges []*change.Change
	for _, c := range pack.Changes {
		if c.ID().ClientSeq() > cp.ClientSeq {
			serverSeq := docInfo.IncreaseServerSeq()
			cp = cp.NextServerSeq(serverSeq)
			c.SetServerSeq(serverSeq)
			pushedChanges = append(pushedChanges, c)
		} else {
			log.Logger.Warnf("change is rejected: %d vs %d ", c.ID().ClientSeq(), cp.ClientSeq)
		}

		cp = cp.SyncClientSeq(c.ClientSeq())
	}

	if len(pack.Changes) > 0 {
		log.Logger.Infof(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.ID,
			len(pushedChanges),
			docInfo.Key,
			len(pack.Changes)-len(pushedChanges),
			initialServerSeq,
			docInfo.ServerSeq,
			cp.String(),
		)
	}

	return cp, pushedChanges, nil
}

func pullPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	requestPack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*change.Pack, error) {
	docKey, err := docInfo.GetKey()
	if err != nil {
		return nil, err
	}

	if initialServerSeq < requestPack.Checkpoint.ServerSeq {
		return nil, errors.Wrapf(
			ErrInvalidServerSeq,
			"server seq(initial %d, request pack %d)",
			initialServerSeq,
			requestPack.Checkpoint.ServerSeq,
		)
	}

	if initialServerSeq-requestPack.Checkpoint.ServerSeq < be.Config.SnapshotThreshold {
		pulledCP, pulledChanges, err := pullChanges(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
		if err != nil {
			return nil, err
		}
		return change.NewPack(docKey, pulledCP, pulledChanges, nil), nil
	}

	pulledCP, snapshot, err := pullSnapshot(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}

	be.Metrics.SetPushPullSnapshotBytes(len(snapshot))

	return change.NewPack(docKey, pulledCP, nil, snapshot), nil
}

func pullChanges(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	requestPack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	pulledChanges, err := be.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		requestPack.Checkpoint.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return nil, nil, err
	}

	pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)

	if len(pulledChanges) > 0 {
		log.Logger.Infof(
			"PULL: '%s' pulls %d changes(%d~%d) from '%s', cp: %s",
			clientInfo.ID,
			len(pulledChanges),
			pulledChanges[0].ServerSeq(),
			pulledChanges[len(pulledChanges)-1].ServerSeq(),
			docInfo.Key,
			pulledCP.String(),
		)
	}

	return pulledCP, pulledChanges, nil
}

func pullSnapshot(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	pack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []byte, error) {
	snapshotInfo, err := be.DB.FindLastSnapshotInfo(ctx, docInfo.ID)
	if err != nil {
		return nil, nil, err
	}

	if snapshotInfo.ServerSeq >= initialServerSeq {
		pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)
		log.Logger.Infof(
			"PULL: '%s' pulls snapshot without changes from '%s', cp: %s",
			clientInfo.ID,
			docInfo.Key,
			pulledCP.String(),
		)
		return pushedCP.NextServerSeq(docInfo.ServerSeq), snapshotInfo.Snapshot, nil
	}

	docKey, err := docInfo.GetKey()
	if err != nil {
		return nil, nil, err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docKey.Collection,
		docKey.Document,
		snapshotInfo.ServerSeq,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return nil, nil, err
	}

	changes, err := be.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		snapshotInfo.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return nil, nil, err
	}

	if err := doc.ApplyChangePack(change.NewPack(
		docKey,
		checkpoint.Initial.NextServerSeq(docInfo.ServerSeq),
		changes,
		nil,
	)); err != nil {
		return nil, nil, err
	}

	pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)

	log.Logger.Infof(
		"PULL: '%s' pulls snapshot with changes(%d~%d) from '%s', cp: %s",
		clientInfo.ID,
		pack.Checkpoint.ServerSeq+1,
		initialServerSeq,
		docInfo.Key,
		pulledCP.String(),
	)

	snapshot, err := converter.ObjectToBytes(doc.RootObject())
	if err != nil {
		return nil, nil, err
	}

	return pulledCP, snapshot, nil
}

func storeSnapshot(
	ctx context.Context,
	be *backend.Backend,
	docInfo *db.DocInfo,
) error {
	start := gotime.Now()

	// 01. get the last snapshot of this docInfo
	// TODO: For performance issue, we only need to read the snapshot's metadata.
	snapshotInfo, err := be.DB.FindLastSnapshotInfo(ctx, docInfo.ID)
	if err != nil {
		return err
	}

	if snapshotInfo.ServerSeq >= docInfo.ServerSeq {
		return nil
	}
	if docInfo.ServerSeq-snapshotInfo.ServerSeq < be.Config.SnapshotInterval {
		return nil
	}

	// 02. retrieve the changes between last snapshot and current docInfo
	changes, err := be.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		snapshotInfo.ServerSeq+1,
		docInfo.ServerSeq,
	)
	if err != nil {
		return err
	}

	// 03. create document instance of the docInfo
	docKey, err := docInfo.GetKey()
	if err != nil {
		return err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docKey.Collection,
		docKey.Document,
		snapshotInfo.ServerSeq,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return err
	}

	if err := doc.ApplyChangePack(change.NewPack(
		docKey,
		checkpoint.Initial.NextServerSeq(docInfo.ServerSeq),
		changes,
		nil,
	)); err != nil {
		return err
	}

	// 04. save the snapshot of the docInfo
	if err := be.DB.CreateSnapshotInfo(ctx, docInfo.ID, doc); err != nil {
		return err
	}

	log.Logger.Infof(
		"SNAP: '%s', serverSeq:%d %s",
		docInfo.Key,
		doc.Checkpoint().ServerSeq,
		gotime.Since(start),
	)
	be.Metrics.ObservePushPullSnapshotDurationSeconds(gotime.Since(start).Seconds())
	return nil
}
