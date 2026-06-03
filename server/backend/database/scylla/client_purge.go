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

	"github.com/yorkie-team/yorkie/api/types"
)

// PurgeDocument purges the given document and its per-document data across
// both stores. MongoDB always owns the DocInfo row itself, and any per-doc
// tables that are not enabled on ScyllaDB also live on MongoDB; the scylla
// side purges only the table groups enabled in Config.Tables.
func (c *Client) PurgeDocument(
	ctx context.Context,
	docRefKey types.DocRefKey,
) (map[string]int64, error) {
	// Mongo handles the documents row, plus any per-doc tables still on Mongo.
	// DeleteMany is idempotent on empty collections, so this is safe even when
	// changes/snapshots/VV all live on ScyllaDB.
	counts, err := c.mongo.PurgeDocument(ctx, docRefKey)
	if err != nil {
		return nil, err
	}

	scyllaCounts, err := c.purgeDocumentInternals(ctx, docRefKey.ProjectID, docRefKey.DocID)
	if err != nil {
		return nil, err
	}
	for k, v := range scyllaCounts {
		counts[k] = v
	}
	return counts, nil
}

// purgeDocumentInternals purges per-document data from ScyllaDB-resident
// tables only. Tables that are not enabled on ScyllaDB are skipped — their
// rows live on MongoDB and are cleared by mongo.PurgeDocumentInternals or
// mongo.PurgeDocument.
func (c *Client) purgeDocumentInternals(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
) (map[string]int64, error) {
	counts := make(map[string]int64)
	docRefKey := types.DocRefKey{ProjectID: projectID, DocID: docID}

	if c.tables.Changes {
		c.changeCache.Remove(docRefKey)
		c.presenceCache.Remove(docRefKey)

		var changesCount int64
		if err := c.session.Query(`
	SELECT COUNT(*) FROM changes
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Scan(&changesCount); err != nil {
			return nil, fmt.Errorf("purge changes of %s: %w", docID, err)
		}
		if err := c.session.Query(`
	DELETE FROM changes
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Exec(); err != nil {
			return nil, fmt.Errorf("purge changes of %s: %w", docID, err)
		}
		counts["changes"] = changesCount
	}

	if c.tables.Snapshots {
		var snapshotsCount int64
		if err := c.session.Query(`
	SELECT COUNT(*) FROM snapshots
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Scan(&snapshotsCount); err != nil {
			return nil, fmt.Errorf("purge snapshots of %s: %w", docID, err)
		}
		if err := c.session.Query(`
	DELETE FROM snapshots
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Exec(); err != nil {
			return nil, fmt.Errorf("purge snapshots of %s: %w", docID, err)
		}
		counts["snapshots"] = snapshotsCount
	}

	if c.tables.VersionVectors {
		c.vectorCache.Remove(docRefKey)

		var vvCount int64
		if err := c.session.Query(`
	SELECT COUNT(*) FROM versionvectors
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Scan(&vvCount); err != nil {
			return nil, fmt.Errorf("purge version vectors of %s: %w", docID, err)
		}
		if err := c.session.Query(`
	DELETE FROM versionvectors
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Exec(); err != nil {
			return nil, fmt.Errorf("purge version vectors of %s: %w", docID, err)
		}
		counts["versionvectors"] = vvCount
	}

	if c.tables.Clients {
		// doc_clients: partition is (project_id, doc_id), so direct delete.
		iter := c.session.Query(`
	SELECT client_id FROM doc_clients
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Iter()

		var docClientsCount int64
		var clientID string
		var clientIDs []string
		for iter.Scan(&clientID) {
			clientIDs = append(clientIDs, clientID)
			docClientsCount++
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("purge doc_clients of %s: %w", docID, err)
		}

		if err := c.session.Query(`
	DELETE FROM doc_clients
	WHERE project_id = ? AND doc_id = ?`,
			projectID, docID,
		).WithContext(ctx).Exec(); err != nil {
			return nil, fmt.Errorf("purge doc_clients of %s: %w", docID, err)
		}
		counts["doc_clients"] = docClientsCount

		// client_documents: partition is (project_id, client_id), so delete
		// each (client_id, doc_id) row individually.
		for _, cid := range clientIDs {
			if err := c.session.Query(`
		DELETE FROM client_documents
		WHERE project_id = ? AND client_id = ? AND doc_id = ?`,
				projectID, cid, docID,
			).WithContext(ctx).Exec(); err != nil {
				return nil, fmt.Errorf("purge client_documents of %s/%s: %w", cid, docID, err)
			}
		}
	}

	return counts, nil
}
