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

package scylla

import (
	"context"
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// CreateSnapshotInfo stores the snapshot of the given document.
func (c *Client) CreateSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	doc *document.InternalDocument,
) error {
	if !c.tables.Snapshots {
		return c.mongo.CreateSnapshotInfo(ctx, docRefKey, doc)
	}

	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	if err := c.session.Query(`
	INSERT INTO snapshots (
		"_id",
		project_id,
		doc_id,
		server_seq,
		lamport,
		version_vector,
		snapshot,
		created_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		types.NewID(),
		docRefKey.ProjectID,
		docRefKey.DocID,
		doc.Checkpoint().ServerSeq,
		doc.Lamport(),
		ToScyllaVersionVector(doc.VersionVector()),
		snapshot,
		gotime.Now(),
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("create snapshot of %s: %w", docRefKey, err)
	}

	return nil
}

// FindSnapshotInfo returns the snapshot info of the given DocRefKey and serverSeq.
func (c *Client) FindSnapshotInfo(
	ctx context.Context,
	docKey types.DocRefKey,
	serverSeq int64,
) (*database.SnapshotInfo, error) {
	if !c.tables.Snapshots {
		return c.mongo.FindSnapshotInfo(ctx, docKey, serverSeq)
	}

	info := &database.SnapshotInfo{}

	query := `
	SELECT
		"_id",
		project_id,
		doc_id,
		server_seq,
		lamport,
		version_vector,
		snapshot,
		created_at
	FROM snapshots
	WHERE project_id = ? AND doc_id = ? AND server_seq = ?`

	var (
		idStr        string
		projectIDStr string
		docIDStr     string
		serverSeqVal int64
		lamport      int64
		vvMap        map[string]int64
		snapBytes    []byte
		createdAt    gotime.Time
	)

	iter := c.session.Query(
		query,
		docKey.ProjectID,
		docKey.DocID,
		serverSeq,
	).WithContext(ctx).Iter()

	if iter.Scan(
		&idStr,
		&projectIDStr,
		&docIDStr,
		&serverSeqVal,
		&lamport,
		&vvMap,
		&snapBytes,
		&createdAt,
	) {
		info = &database.SnapshotInfo{
			ID:            types.ID(idStr),
			ProjectID:     types.ID(projectIDStr),
			DocID:         types.ID(docIDStr),
			ServerSeq:     serverSeqVal,
			Lamport:       lamport,
			VersionVector: FromScyllaVersionVector(vvMap),
			Snapshot:      snapBytes,
			CreatedAt:     createdAt,
		}
	}

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("find snapshot before %d of %s: %w", serverSeq, docKey, err)
	}

	return info, nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (c *Client) FindClosestSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	if !c.tables.Snapshots {
		return c.mongo.FindClosestSnapshotInfo(ctx, docRefKey, serverSeq, includeSnapshot)
	}

	info := &database.SnapshotInfo{}

	query := `
	SELECT
		"_id",
		project_id,
		doc_id,
		server_seq,
		lamport,
		version_vector,
		snapshot,
		created_at
	FROM snapshots
	WHERE project_id = ? AND doc_id = ? AND server_seq <= ?
	ORDER BY server_seq DESC
	LIMIT 1`

	var (
		idStr        string
		projectIDStr string
		docIDStr     string
		serverSeqVal int64
		lamport      int64
		vvMap        map[string]int64
		snapBytes    []byte
		createdAt    gotime.Time
	)

	iter := c.session.Query(
		query,
		docRefKey.ProjectID,
		docRefKey.DocID,
		serverSeq,
	).WithContext(ctx).Iter()

	found := iter.Scan(
		&idStr,
		&projectIDStr,
		&docIDStr,
		&serverSeqVal,
		&lamport,
		&vvMap,
		&snapBytes,
		&createdAt,
	)

	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("find snapshot before %d of %s: %w", serverSeq, docRefKey, err)
	}

	if !found {
		info.VersionVector = time.NewVersionVector()
		return info, nil
	}

	if includeSnapshot {
		info.Snapshot = snapBytes
	}

	info.ID = types.ID(idStr)
	info.ProjectID = types.ID(projectIDStr)
	info.DocID = types.ID(docIDStr)
	info.ServerSeq = serverSeqVal
	info.Lamport = lamport
	info.VersionVector = FromScyllaVersionVector(vvMap)
	info.CreatedAt = createdAt

	return info, nil
}
