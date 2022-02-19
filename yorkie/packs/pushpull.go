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
	"github.com/yorkie-team/yorkie/yorkie/logging"
)

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.New("invalid server seq")
)

// pushChanges returns the changes excluding already saved in DB.
func pushChanges(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	reqPack *change.Pack,
	initialServerSeq uint64,
) (change.Checkpoint, []*change.Change) {
	cp := clientInfo.Checkpoint(docInfo.ID)

	var pushedChanges []*change.Change
	for _, cn := range reqPack.Changes {
		if cn.ID().ClientSeq() > cp.ClientSeq {
			serverSeq := docInfo.IncreaseServerSeq()
			cp = cp.NextServerSeq(serverSeq)
			cn.SetServerSeq(serverSeq)
			pushedChanges = append(pushedChanges, cn)
		} else {
			logging.From(ctx).Warnf(
				"change already pushed, clientSeq: %d, cp: %d",
				cn.ID().ClientSeq(),
				cp.ClientSeq,
			)
		}

		cp = cp.SyncClientSeq(cn.ClientSeq())
	}

	if len(reqPack.Changes) > 0 {
		logging.From(ctx).Infof(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.ID,
			len(pushedChanges),
			docInfo.CombinedKey,
			len(reqPack.Changes)-len(pushedChanges),
			initialServerSeq,
			docInfo.ServerSeq,
			cp.String(),
		)
	}

	return cp, pushedChanges
}

func pullPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq uint64,
) (*ServerPack, error) {
	docKey, err := docInfo.Key()
	if err != nil {
		return nil, err
	}

	if initialServerSeq < reqPack.Checkpoint.ServerSeq {
		return nil, fmt.Errorf(
			"serverSeq of CP greater than serverSeq of clientInfo(clientInfo %d, cp %d): %w",
			initialServerSeq,
			reqPack.Checkpoint.ServerSeq,
			ErrInvalidServerSeq,
		)
	}

	// Pull changes from DB if the size of changes for the response is less than the snapshot threshold.
	if initialServerSeq-reqPack.Checkpoint.ServerSeq < be.Config.SnapshotThreshold {
		cpAfterPull, pulledChanges, err := pullChangeInfos(ctx, be, clientInfo, docInfo, reqPack, cpAfterPush, initialServerSeq)
		if err != nil {
			return nil, err
		}
		return NewServerPack(docKey, cpAfterPull, pulledChanges, nil), err
	}

	// Build document from DB if the size of changes for the response is greater than the snapshot threshold.
	doc, err := buildDocForServerSeq(ctx, be, docInfo, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// Apply changes that are in the request pack.
	if reqPack.HasChanges() {
		if err := doc.ApplyChangePack(change.NewPack(
			docKey,
			doc.Checkpoint().NextServerSeq(docInfo.ServerSeq),
			reqPack.Changes,
			nil,
		)); err != nil {
			return nil, err
		}
	}
	cpAfterPull := cpAfterPush.NextServerSeq(docInfo.ServerSeq)

	snapshot, err := converter.ObjectToBytes(doc.RootObject())
	if err != nil {
		return nil, err
	}

	logging.From(ctx).Infof(
		"PULL: '%s' build snapshot with changes(%d~%d) from '%s', cp: %s",
		clientInfo.ID,
		reqPack.Checkpoint.ServerSeq+1,
		initialServerSeq,
		docInfo.CombinedKey,
		cpAfterPull.String(),
	)

	return NewServerPack(docKey, cpAfterPull, nil, snapshot), err
}

func pullChangeInfos(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq uint64,
) (change.Checkpoint, []*db.ChangeInfo, error) {
	pulledChanges, err := be.DB.FindChangeInfosBetweenServerSeqs(
		ctx,
		docInfo.ID,
		reqPack.Checkpoint.ServerSeq+1,
		initialServerSeq,
	)
	if err != nil {
		return change.InitialCheckpoint, nil, err
	}

	cpAfterPull := cpAfterPush.NextServerSeq(docInfo.ServerSeq)

	if len(pulledChanges) > 0 {
		logging.From(ctx).Infof(
			"PULL: '%s' pulls %d changes(%d~%d) from '%s', cp: %s",
			clientInfo.ID,
			len(pulledChanges),
			pulledChanges[0].ServerSeq,
			pulledChanges[len(pulledChanges)-1].ServerSeq,
			docInfo.CombinedKey,
			cpAfterPull.String(),
		)
	}

	return cpAfterPull, pulledChanges, nil
}

// buildDocForServerSeq returns a new document for the given serverSeq.
func buildDocForServerSeq(
	ctx context.Context,
	be *backend.Backend,
	docInfo *db.DocInfo,
	serverSeq uint64,
) (*document.InternalDocument, error) {
	snapshotInfo, err := be.DB.FindClosestSnapshotInfo(ctx, docInfo.ID, serverSeq)
	if err != nil {
		return nil, err
	}

	docKey, err := docInfo.Key()
	if err != nil {
		return nil, err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docKey,
		snapshotInfo.ServerSeq,
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
		docKey,
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
