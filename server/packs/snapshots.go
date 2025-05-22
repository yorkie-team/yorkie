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
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// storeSnapshot stores the snapshot of the document in the database.
func storeSnapshot(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
) error {
	// 01. get the closest snapshot's metadata of this docInfo
	docRefKey := docInfo.RefKey()
	snapshotInfo, err := be.DB.FindClosestSnapshotInfo(
		ctx,
		docRefKey,
		docInfo.ServerSeq,
		false,
	)
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
		docRefKey,
		snapshotInfo.ServerSeq+1,
		docInfo.ServerSeq,
	)
	if err != nil {
		return err
	}

	// 03. Fetch the snapshot info including its snapshot.
	if snapshotInfo.ID != "" {
		snapshotInfo, err = be.DB.FindSnapshotInfoByRefKey(ctx, snapshotInfo.RefKey())
		if err != nil {
			return err
		}
	}

	doc, err := document.NewInternalDocumentFromSnapshot(
		docInfo.Key,
		snapshotInfo.ServerSeq,
		snapshotInfo.Lamport,
		snapshotInfo.VersionVector,
		snapshotInfo.Snapshot,
	)
	if err != nil {
		return err
	}

	pack := change.NewPack(
		docInfo.Key,
		change.InitialCheckpoint.NextServerSeq(docInfo.ServerSeq),
		changes,
		nil,
		nil,
	)

	if err := doc.ApplyChangePack(pack, be.Config.SnapshotDisableGC); err != nil {
		return err
	}

	// 05. save the snapshot of the docInfo
	if err := be.DB.CreateSnapshotInfo(
		ctx,
		docRefKey,
		doc,
	); err != nil {
		return err
	}

	logging.From(ctx).Infof(
		"SNAP: '%s', serverSeq: %d",
		docInfo.Key,
		doc.Checkpoint().ServerSeq,
	)

	return nil
}
