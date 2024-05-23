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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrInvalidServerSeq is returned when the given server seq greater than
	// the initial server seq.
	ErrInvalidServerSeq = errors.New("invalid server seq")
)

// pushChanges returns the changes excluding already saved in DB.
func pushChanges(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	initialServerSeq int64,
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
		logging.From(ctx).Debugf(
			"PUSH: '%s' pushes %d changes into '%s', rejected %d changes, serverSeq: %d -> %d, cp: %s",
			clientInfo.Key,
			len(pushedChanges),
			docInfo.Key,
			len(reqPack.Changes)-len(pushedChanges),
			initialServerSeq,
			docInfo.ServerSeq,
			cp,
		)
	}

	return cp, pushedChanges
}

func pullPack(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
	reqPack *change.Pack,
	cpAfterPush change.Checkpoint,
	initialServerSeq int64,
	mode types.SyncMode,
) (*ServerPack, error) {
	// If the client is push-only, it does not need to pull changes.
	// So, just return the checkpoint with server seq after pushing changes.
	if mode == types.SyncModePushOnly {
		return NewServerPack(docInfo.Key, change.Checkpoint{
			ServerSeq: reqPack.Checkpoint.ServerSeq,
			ClientSeq: cpAfterPush.ClientSeq,
		}, nil, nil), nil
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
	// Build document from DB if the size of changes for the response is greater than the snapshot threshold.
	doc, err := BuildDocumentForServerSeq(ctx, be, docInfo, initialServerSeq)
	if err != nil {
		return nil, err
	}

	// Apply changes that are in the request pack.
	if reqPack.HasChanges() {
		if err := doc.ApplyChangePack(change.NewPack(
			docInfo.Key,
			doc.Checkpoint().NextServerSeq(docInfo.ServerSeq),
			reqPack.Changes,
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

	return NewServerPack(docInfo.Key, cpAfterPull, nil, snapshot), err
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
