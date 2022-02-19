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

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
	"github.com/yorkie-team/yorkie/yorkie/logging"
)

func storeSnapshot(
	ctx context.Context,
	be *backend.Backend,
	docInfo *db.DocInfo,
	minSyncedTicket *time.Ticket,
) error {
	// 01. get the closest snapshot of this docInfo
	// TODO: For performance issue, we only need to read the snapshot's metadata.
	snapshotInfo, err := be.DB.FindClosestSnapshotInfo(ctx, docInfo.ID, docInfo.ServerSeq)
	if err != nil {
		return err
	}
	if snapshotInfo.ServerSeq == docInfo.ServerSeq {
		return nil
	}
	if docInfo.ServerSeq-snapshotInfo.ServerSeq < be.Config.SnapshotInterval {
		return nil
	}

	// 02. retrieve the changes between last snapshot and current docInfo
	changes, err := be.DB.FindChangesBetweenServerSeqs(
		ctx,
		docInfo.ID,
		snapshotInfo.ServerSeq+1,
		docInfo.ServerSeq,
	)
	if err != nil {
		return err
	}

	// 03. create document instance of the docInfo
	docKey, err := docInfo.Key()
	if err != nil {
		return err
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docKey,
		snapshotInfo.ServerSeq,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return err
	}

	pack := change.NewPack(
		docKey,
		change.InitialCheckpoint.NextServerSeq(docInfo.ServerSeq),
		changes,
		nil,
	)
	pack.MinSyncedTicket = minSyncedTicket

	if err := doc.ApplyChangePack(pack); err != nil {
		return err
	}

	// 04. save the snapshot of the docInfo
	if err := be.DB.CreateSnapshotInfo(ctx, docInfo.ID, doc); err != nil {
		return err
	}

	logging.From(ctx).Infof(
		"SNAP: '%s', serverSeq: %d",
		docInfo.CombinedKey,
		doc.Checkpoint().ServerSeq,
	)
	return nil
}
