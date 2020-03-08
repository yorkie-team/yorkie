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

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/log"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/pubsub"
	"github.com/yorkie-team/yorkie/yorkie/types"
)

func PushPull(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *types.ClientInfo,
	docInfo *types.DocInfo,
	pack *change.Pack,
) (*change.Pack, error) {
	// TODO Changes may be reordered or missing during communication on the network.
	// We should check the change.pack with checkpoint to make sure the changes are in the correct order.

	// TODO We need to prevent the same document from being modified at the same time.
	// For this, We may want to consider introducing distributed lock or DB transaction.
	// To improve read performance, we can also consider something like read-write lock,
	// because simple read operations do not break consistency.
	initialServerSeq := docInfo.ServerSeq

	// 01. push changes.
	pushedCP, pushedChanges, err := pushChanges(clientInfo, docInfo, pack, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// 02. pull changes.
	pulledCP, pulledChanges, err := pullChanges(ctx, be, clientInfo, docInfo, pack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}

	if err := clientInfo.UpdateCheckpoint(docInfo.ID, pulledCP); err != nil {
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

	// 04. publish document change event.
	if pack.HasChanges() {
		be.Publish(
			time.ActorIDFromHex(clientInfo.ID.Hex()),
			pack.DocumentKey.BSONKey(),
			pubsub.Event{
				Type: pubsub.DocumentChangeEvent,
				Value: pack.DocumentKey.BSONKey(),
			},
		)
	}

	docKey, err := key.FromBSONKey(docInfo.Key)
	if err != nil {
		return nil, err
	}

	return change.NewPack(
		docKey,
		pulledCP,
		pulledChanges,
	), nil
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
			log.Logger.Warnf("change is rejected: %v", c)
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
