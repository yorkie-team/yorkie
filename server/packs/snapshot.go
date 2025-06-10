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
	"fmt"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// SnapshotKey creates a new sync.Key of Snapshot for the given document.
func SnapshotKey(projectID types.ID, docKey key.Key) sync.Key {
	return sync.NewKey(fmt.Sprintf("snapshot-%s-%s", projectID, docKey))
}

// BuildDocForCheckpoint returns a new document for the given checkpoint.
func BuildDocForCheckpoint(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	cp change.Checkpoint,
	actorID time.ActorID,
) (*document.Document, error) {
	internalDoc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, cp.ServerSeq)
	if err != nil {
		return nil, err
	}

	internalDoc.SetActor(actorID)
	internalDoc.SyncCheckpoint(cp.ServerSeq, cp.ClientSeq)
	return internalDoc.ToDocument(), nil
}

// BuildInternalDocForServerSeq returns a new document for the given serverSeq.
func BuildInternalDocForServerSeq(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
	serverSeq int64,
) (*document.InternalDocument, error) {
	docKey := docInfo.RefKey()
	var doc *document.InternalDocument
	var err error
	if cached, ok := be.Cache.Snapshot.Get(docKey); ok {
		doc, err = cached.DeepCopy()
		if err != nil {
			return nil, err
		}
	}

	// NOTE(hackerwins): If the document is already in the cache, we can skip
	// the database query and use the cached document. If the document's server
	// sequence in the cache is greater than the given server sequence, we can't
	// build the document from the document. In this case, we need to
	// query the database to get the closest snapshot information.
	if doc == nil || serverSeq < doc.Checkpoint().ServerSeq {
		snapshotInfo, err := be.DB.FindClosestSnapshotInfo(
			ctx,
			docKey,
			serverSeq,
			true,
		)
		if err != nil {
			return nil, err
		}

		doc, err = document.NewInternalDocumentFromSnapshot(
			docInfo.Key,
			snapshotInfo.ServerSeq,
			snapshotInfo.Lamport,
			snapshotInfo.VersionVector,
			snapshotInfo.Snapshot,
		)
		if err != nil {
			return nil, err
		}
	}

	changes, err := be.DB.FindChangesBetweenServerSeqs(
		ctx,
		docKey,
		doc.Checkpoint().ServerSeq+1,
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
		nil,
	), be.Config.SnapshotDisableGC); err != nil {
		return nil, err
	}
	if !be.Config.SnapshotDisableGC {
		vector, err := be.DB.GetMinVersionVector(
			ctx,
			docKey,
			doc.VersionVector(),
		)
		if err != nil {
			return nil, err
		}
		if _, err := doc.GarbageCollect(vector); err != nil {
			return nil, err
		}
	}

	// NOTE(hackerwins): Store the last accessed document in the cache.
	be.Cache.Snapshot.Add(docKey, doc)

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Debugf(
			"after apply %d changes: elements: %d removeds: %d, %s",
			len(changes),
			doc.Root().ElementMapLen(),
			doc.Root().GarbageElementLen(),
			doc.RootObject().Marshal(),
		)
	}

	clone, err := doc.DeepCopy()
	if err != nil {
		return nil, err
	}

	return clone, nil
}

// storeSnapshot stores the snapshot of the document in the database.
func storeSnapshot(
	ctx context.Context,
	be *backend.Backend,
	docInfo *database.DocInfo,
) error {
	// NOTE(hackerwins): If the snapshot is already being created by another routine,
	// it is not necessary to recreate it, so we can skip it.
	locker, ok := be.Lockers.LockerWithTryLock(SnapshotKey(docInfo.ProjectID, docInfo.Key))
	if !ok {
		return nil
	}
	defer locker.Unlock()

	start := gotime.Now()

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

	be.Metrics.ObservePushPullSnapshotDurationSeconds(gotime.Since(start).Seconds())

	return nil
}
