/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package mongo

import (
	"context"
	"fmt"
	gotime "time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// PoC helpers used by the ScyllaDB delegating client. The scylla.Client
// keeps changes/snapshots/version-vectors/clients in ScyllaDB but still
// stores DocInfo in MongoDB. These helpers expose the doc-mutation parts
// of CreateChangeInfos/CompactChangeInfos/PurgeDocument so the scylla
// layer can update the MongoDB-side DocInfo without duplicating BSON
// logic.

// UpdateDocAfterChanges updates DocInfo after a CreateChangeInfos call that
// already wrote the changes themselves elsewhere (e.g., ScyllaDB). On
// success the docCache is refreshed with the mutated docInfo so concurrent
// readers see the post-update state. Mirrors the doc-update tail of
// mongo.Client.CreateChangeInfos.
//
// The docInfo argument MUST be the locally-mutated copy whose ServerSeq has
// already been bumped to the post-write value; initialServerSeq is the
// pre-write server_seq used as the CAS filter.
//
// Why caller passes docInfo (and not just numbers): the cache must be
// rewritten atomically with the DB commit. If the helper only had
// (initialServerSeq, newServerSeq) it could either Remove the cache (which
// opens a window for stale repopulation by lock-free readers, e.g., empty
// PushPullChanges) or skip cache management entirely (also stale). Passing
// docInfo lets the helper docCache.Add the new state, exactly like mongo's
// own CreateChangeInfos does.
func (c *Client) UpdateDocAfterChanges(
	ctx context.Context,
	docInfo *database.DocInfo,
	initialServerSeq int64,
	hasOperations bool,
	isRemoved bool,
) error {
	now := gotime.Now()
	refKey := docInfo.RefKey()

	updateFields := bson.M{
		"server_seq": docInfo.ServerSeq,
	}
	if hasOperations {
		updateFields["updated_at"] = now
	}
	if isRemoved {
		updateFields["removed_at"] = now
	}

	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"project_id": refKey.ProjectID,
		"_id":        refKey.DocID,
		"server_seq": initialServerSeq,
	}, bson.M{
		"$set": updateFields,
	})
	if err != nil {
		c.docCache.Remove(refKey)
		return fmt.Errorf("update doc after changes of %s: %w", refKey, err)
	}
	if res.MatchedCount == 0 {
		c.docCache.Remove(refKey)
		return fmt.Errorf("update doc after changes of %s: %w", refKey, database.ErrConflictOnUpdate)
	}

	if isRemoved {
		docInfo.RemovedAt = now
	}
	c.docCache.Add(refKey, docInfo)
	return nil
}

// CompactDocAfterChanges updates DocInfo after compaction, bumping the
// epoch and setting compacted_at. Mirrors the doc-update tail of
// mongo.Client.CompactChangeInfos.
func (c *Client) CompactDocAfterChanges(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
	lastServerSeq int64,
	newServerSeq int64,
) error {
	refKey := types.DocRefKey{ProjectID: projectID, DocID: docID}
	c.docCache.Remove(refKey)

	res, err := c.collection(ColDocuments).UpdateOne(ctx, bson.M{
		"project_id": projectID,
		"_id":        docID,
		"server_seq": lastServerSeq,
	}, bson.M{
		"$set": bson.M{
			"server_seq":   newServerSeq,
			"compacted_at": gotime.Now(),
		},
		"$inc": bson.M{
			"epoch": int64(1),
		},
	})
	if err != nil {
		return fmt.Errorf("compact doc of %s: %w", refKey, err)
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("%s: %s: %w", projectID, docID, database.ErrConflictOnUpdate)
	}
	return nil
}

// DeleteDocumentRecord removes only the documents-row for the given
// doc. The scylla layer purges per-doc data from ScyllaDB itself, so
// this helper just drops the matching DocInfo row in MongoDB.
func (c *Client) DeleteDocumentRecord(
	ctx context.Context,
	docRefKey types.DocRefKey,
) error {
	c.docCache.Remove(docRefKey)
	if _, err := c.collection(ColDocuments).DeleteOne(ctx, bson.M{
		"project_id": docRefKey.ProjectID,
		"_id":        docRefKey.DocID,
	}); err != nil {
		return fmt.Errorf("delete document record of %s: %w", docRefKey, err)
	}
	return nil
}

// PurgeDocumentInternals exposes the per-document internals purge
// (changes/snapshots/snapshot bodies/version vectors) without removing
// the DocInfo row. Used by the scylla layer during compaction to also
// clear any rows that still live on MongoDB. Deletes on collections
// that hold no rows are silent no-ops.
func (c *Client) PurgeDocumentInternals(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
) (map[string]int64, error) {
	return c.purgeDocumentInternals(ctx, projectID, docID)
}
