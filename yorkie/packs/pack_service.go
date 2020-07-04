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
	"fmt"
	defaultTime "time"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	pkgTypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/pubsub"
	"github.com/yorkie-team/yorkie/yorkie/types"
)

func PushPull(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	reqPack *change.Pack,
) (*change.Pack, error) {
	// TODO Changes may be reordered or missing during communication on the network.
	// We should check the change.pack with checkpoint to make sure the changes are in the correct order.

	// TODO We need to prevent the same document from being modified at the same time.
	// For this, We may want to consider introducing distributed lock or DB transaction.
	// To improve read performance, we can also consider something like read-write lock,
	// because simple read operations do not break consistency.
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

	// 03. save pushed changes, document info and checkpoint of the client to MongoDB.
	if err := be.Mongo.CreateChangeInfos(ctx, docInfo.ID, pushedChanges); err != nil {
		return nil, err
	}

	if err := be.Mongo.UpdateDocInfo(ctx, docInfo); err != nil {
		return nil, err
	}

	if err := be.Mongo.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo); err != nil {
		return nil, err
	}

	// 04. update and find min synced ticket for garbage collection.
	// NOTE Since the client could not receive the PushPull response,
	//      the requested seq(reqPack) is stored instead of the response seq(resPack).
	respPack.MinSyncedTicket, err = be.Mongo.UpdateAndFindMinSyncedTicket(
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
			actorID := time.ActorIDFromHex(clientInfo.ID.Hex())
			be.PubSub.Publish(
				actorID,
				reqPack.DocumentKey.BSONKey(),
				pubsub.DocEvent{
					Type:   pkgTypes.DocumentsChangeEvent,
					DocKey: reqPack.DocumentKey.BSONKey(),
					ActorID: actorID,
				},
			)

			key := fmt.Sprintf("snapshot-%s", docInfo.Key)
			if err := be.MutexMap.Lock(key); err != nil {
				log.Logger.Error(err)
				return
			}
			defer func() {
				if err := be.MutexMap.Unlock(key); err != nil {
					log.Logger.Error(err)
				}
			}()

			if err := storeSnapshot(context.Background(), be, docInfo); err != nil {
				log.Logger.Error(err)
			}
		})
	}

	return respPack, nil
}

// pushChanges returns the changes excluding already saved in MongoDB.
func pushChanges(
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	cp := clientInfo.GetCheckpoint(docInfo.ID)

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
			clientInfo.ID.Hex(),
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
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	requestPack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*change.Pack, error) {
	docKey, err := docInfo.GetKey()
	if err != nil {
		return nil, err
	}

	if initialServerSeq-requestPack.Checkpoint.ServerSeq < be.Config.SnapshotThreshold {
		pulledCP, pulledChanges, err := pullChanges(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
		if err != nil {
			return nil, err
		}
		return change.NewPack(docKey, pulledCP, pulledChanges, nil), err
	}

	pulledCP, snapshot, err := pullSnapshot(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}
	return change.NewPack(docKey, pulledCP, nil, snapshot), err
}

func pullChanges(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []*change.Change, error) {
	fetchedChanges, err := be.Mongo.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		pack.Checkpoint.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return nil, nil, err
	}

	var pulledChanges []*change.Change
	for _, fetchedChange := range fetchedChanges {
		if fetchedChange.ID().Actor().String() == clientInfo.ID.Hex() {
			continue
		}

		pulledChanges = append(pulledChanges, fetchedChange)
	}

	pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)

	if len(pulledChanges) > 0 {
		log.Logger.Infof(
			"PULL: '%s' pulls %d changes(%d~%d) from '%s', cp: %s",
			clientInfo.ID.Hex(),
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
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
	pushedCP *checkpoint.Checkpoint,
	initialServerSeq uint64,
) (*checkpoint.Checkpoint, []byte, error) {
	snapshotInfo, err := be.Mongo.FindLastSnapshotInfo(ctx, docInfo.ID)
	if err != nil {
		return nil, nil, err
	}

	if snapshotInfo.ServerSeq >= initialServerSeq {
		pulledCP := pushedCP.NextServerSeq(docInfo.ServerSeq)
		log.Logger.Infof(
			"PULL: '%s' pulls snapshot without changes from '%s', cp: %s",
			clientInfo.ID.Hex(),
			docInfo.Key,
			pulledCP.String(),
		)
		return pushedCP.NextServerSeq(docInfo.ServerSeq), snapshotInfo.Snapshot, nil
	}

	docKey, err := docInfo.GetKey()
	if err != nil {
		return nil, nil, err
	}

	doc, err := document.FromSnapshot(
		docKey.Collection,
		docKey.Document,
		snapshotInfo.ServerSeq,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return nil, nil, err
	}

	changes, err := be.Mongo.FindChangeInfosBetweenServerSeqs(
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
		clientInfo.ID.Hex(),
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
	docInfo *types.DocInfo,
) error {
	start := defaultTime.Now()

	// 01. get the last snapshot of this docInfo
	// TODO For performance, we only need to read the snapshot's metadata.
	snapshotInfo, err := be.Mongo.FindLastSnapshotInfo(ctx, docInfo.ID)
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
	changes, err := be.Mongo.FindChangeInfosBetweenServerSeqs(
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
	if err := be.Mongo.CreateSnapshotInfo(ctx, docInfo.ID, doc); err != nil {
		return err
	}

	log.Logger.Infof(
		"SNAP: '%s', serverSeq:%d %s",
		docInfo.Key,
		doc.Checkpoint().ServerSeq,
		defaultTime.Since(start),
	)
	return nil
}
