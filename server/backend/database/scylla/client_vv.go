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
	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// UpdateMinVersionVector updates the version vector of the given client
// and returns the minimum version vector of all clients.
func (c *Client) UpdateMinVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	if !c.tables.VersionVectors {
		return c.mongo.UpdateMinVersionVector(ctx, clientInfo, docRefKey, vector)
	}

	// 01. Update synced version vector of the given client and document.
	needsUpdate := true
	if vvMap, ok := c.vectorCache.Get(docRefKey); ok {
		if existing, ok := vvMap.Get(clientInfo.ID); ok && vector.Equal(existing) {
			needsUpdate = false
		}
	}
	if needsUpdate {
		if err := c.updateVersionVector(ctx, clientInfo, docRefKey, vector); err != nil {
			return nil, err
		}
	}

	// 02. Update current client's version vector.
	if vvMap, ok := c.vectorCache.Get(docRefKey); ok {
		attached, err := clientInfo.IsAttached(docRefKey.DocID)
		if err != nil {
			return nil, err
		}

		if attached {
			vvMap.Upsert(clientInfo.ID, func(value time.VersionVector, exists bool) time.VersionVector {
				return vector
			})
		} else {
			vvMap.Delete(clientInfo.ID)
		}
	}

	// 03. Calculate the minimum version vector of the given document.
	return c.GetMinVersionVector(ctx, docRefKey, vector)
}

// GetMinVersionVector returns the minimum version vector of the given document.
func (c *Client) GetMinVersionVector(
	ctx context.Context,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	if !c.tables.VersionVectors {
		return c.mongo.GetMinVersionVector(ctx, docRefKey, vector)
	}

	if !c.vectorCache.Contains(docRefKey) {
		var infos []database.VersionVectorInfo

		query := `
	SELECT
		client_id,
		version_vector
	FROM versionvectors
	WHERE project_id = ? AND doc_id = ?`

		iter := c.session.Query(
			query,
			docRefKey.ProjectID,
			docRefKey.DocID,
		).WithContext(ctx).Iter()

		for {
			var (
				clientIDStr string
				vvMap       map[string]int64
			)

			if !iter.Scan(&clientIDStr, &vvMap) {
				break
			}

			infos = append(infos, database.VersionVectorInfo{
				ProjectID:     docRefKey.ProjectID,
				DocID:         docRefKey.DocID,
				ClientID:      types.ID(clientIDStr),
				VersionVector: FromScyllaVersionVector(vvMap),
			})
		}

		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("find min version vector: %w", err)
		}

		infoMap := cmap.New[types.ID, time.VersionVector]()
		for i := range infos {
			infoMap.Set(infos[i].ClientID, infos[i].VersionVector)
		}

		c.vectorCache.Add(docRefKey, infoMap)
	}

	vvMap, ok := c.vectorCache.Get(docRefKey)
	if !ok {
		return nil, fmt.Errorf("find min version vector: %w", database.ErrVersionVectorNotFound)
	}

	vals := vvMap.Values()
	vectors := make([]time.VersionVector, len(vals)+1)
	copy(vectors, vals)
	vectors[len(vals)] = vector
	return time.MinVersionVector(vectors...), nil
}

// updateVersionVector updates the given version vector of the given client.
func (c *Client) updateVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) error {
	isAttached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	if !isAttached {
		// Detach: remove the client's version vector entry from Scylla.
		if err := c.session.Query(`
	DELETE FROM versionvectors
	WHERE project_id = ? AND doc_id = ? AND client_id = ?`,
			docRefKey.ProjectID,
			docRefKey.DocID,
			clientInfo.ID,
		).WithContext(ctx).Exec(); err != nil {
			return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
		}
		return nil
	}

	// Upsert the client's version vector in Scylla.
	if err := c.session.Query(`
	INSERT INTO versionvectors (
		project_id,
		doc_id,
		client_id,
		version_vector
	) VALUES (?, ?, ?, ?)`,
		docRefKey.ProjectID,
		docRefKey.DocID,
		clientInfo.ID,
		ToScyllaVersionVector(vector),
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("update version vector of %s: %w", docRefKey, err)
	}

	return nil
}
