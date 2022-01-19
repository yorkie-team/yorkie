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

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/log"
)

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.New("invalid server seq")
)

// pushChanges returns the changes excluding already saved in DB.
func pushChanges(
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	pack *change.Pack,
	initialServerSeq uint64,
) (*change.Checkpoint, []*change.Change, error) {
	cp := clientInfo.Checkpoint(docInfo.ID)

	var pushedChanges []*change.Change
	for _, cn := range pack.Changes {
		if cn.ID().ClientSeq() > cp.ClientSeq {
			serverSeq := docInfo.IncreaseServerSeq()
			cp = cp.NextServerSeq(serverSeq)
			cn.SetServerSeq(serverSeq)
			pushedChanges = append(pushedChanges, cn)
		} else {
			log.Logger.Warnf("change is rejected: %d vs %d ", cn.ID().ClientSeq(), cp.ClientSeq)
		}

		cp = cp.SyncClientSeq(cn.ClientSeq())
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
	pushedCP *change.Checkpoint,
	initialServerSeq uint64,
) (*ServerPack, error) {
	docKey, err := docInfo.GetKey()
	if err != nil {
		return nil, err
	}

	if initialServerSeq < requestPack.Checkpoint.ServerSeq {
		return nil, fmt.Errorf(
			"server seq(initial %d, request pack %d): %w",
			initialServerSeq,
			requestPack.Checkpoint.ServerSeq,
			ErrInvalidServerSeq,
		)
	}

	if initialServerSeq-requestPack.Checkpoint.ServerSeq < be.Config.SnapshotThreshold {
		pulledCP, pulledChanges, err := pullChangeInfos(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
		if err != nil {
			return nil, err
		}
		return NewServerPack(docKey, pulledCP, pulledChanges, nil), err
	}

	pulledCP, snapshot, err := pullSnapshot(ctx, be, clientInfo, docInfo, requestPack, pushedCP, initialServerSeq)
	if err != nil {
		return nil, err
	}

	return NewServerPack(docKey, pulledCP, nil, snapshot), err
}

func pullChangeInfos(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	requestPack *change.Pack,
	pushedCP *change.Checkpoint,
	initialServerSeq uint64,
) (*change.Checkpoint, []*db.ChangeInfo, error) {
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
			pulledChanges[0].ServerSeq,
			pulledChanges[len(pulledChanges)-1].ServerSeq,
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
	pushedCP *change.Checkpoint,
	initialServerSeq uint64,
) (*change.Checkpoint, []byte, error) {
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

	// TODO(hackerwins): If the Snapshot is missing, we may have a very large
	// number of changes to read at once here. We need to split changes by a
	// certain size (e.g. 100) and read and gradually reflect it into the document.
	changes, err := be.DB.FindChangesBetweenServerSeqs(
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
		change.InitialCheckpoint.NextServerSeq(docInfo.ServerSeq),
		changes,
		nil,
	)); err != nil {
		return nil, nil, err
	}

	if log.Core.Enabled(zap.DebugLevel) {
		log.Logger.Debugf(
			"after apply %d changes: elements: %d removeds: %d, %s",
			len(pack.Changes),
			doc.Root().ElementMapLen(),
			doc.Root().RemovedElementLen(),
			doc.RootObject().Marshal(),
		)
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
