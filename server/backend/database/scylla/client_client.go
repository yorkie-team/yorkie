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
	"sort"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// readClientInfo reads a full ClientInfo from ScyllaDB by combining
// data from the clients table and the client_documents table.
func (c *Client) readClientInfo(
	ctx context.Context,
	projectID types.ID,
	clientID types.ID,
) (*database.ClientInfo, error) {
	var id, projID, clientKey, status string
	var metadata map[string]string
	var createdAt, updatedAt gotime.Time

	if err := c.session.Query(`
		SELECT "_id", project_id, key, status, metadata, created_at, updated_at
		FROM clients
		WHERE project_id = ? AND "_id" = ?`,
		string(projectID), string(clientID),
	).WithContext(ctx).Scan(&id, &projID, &clientKey, &status, &metadata, &createdAt, &updatedAt); err != nil {
		if err.Error() == "not found" {
			return nil, fmt.Errorf("find client %s/%s: %w", projectID, clientID, database.ErrClientNotFound)
		}
		return nil, fmt.Errorf("find client %s/%s: %w", projectID, clientID, err)
	}

	docs := make(database.ClientDocInfoMap)
	iter := c.session.Query(`
		SELECT doc_id, status, server_seq, client_seq
		FROM client_documents
		WHERE project_id = ? AND client_id = ?`,
		string(projectID), string(clientID),
	).WithContext(ctx).Iter()

	var docID, docStatus string
	var serverSeq int64
	var clientSeq int
	for iter.Scan(&docID, &docStatus, &serverSeq, &clientSeq) {
		docs[types.ID(docID)] = &database.ClientDocInfo{
			Status:    docStatus,
			ServerSeq: serverSeq,
			ClientSeq: uint32(clientSeq),
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("find client docs %s/%s: %w", projectID, clientID, err)
	}

	if metadata == nil {
		metadata = make(map[string]string)
	}

	return &database.ClientInfo{
		ID:        types.ID(id),
		ProjectID: types.ID(projID),
		Key:       clientKey,
		Status:    status,
		Documents: docs,
		Metadata:  metadata,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}, nil
}

// ActivateClient activates the client of the given key.
func (c *Client) ActivateClient(
	ctx context.Context,
	projectID types.ID,
	key string,
	metadata map[string]string,
) (*database.ClientInfo, error) {
	if !c.tables.Clients {
		return c.mongo.ActivateClient(ctx, projectID, key, metadata)
	}

	now := gotime.Now()

	info := &database.ClientInfo{
		ID:        types.NewID(),
		ProjectID: projectID,
		Key:       key,
		Status:    database.ClientActivated,
		Documents: make(database.ClientDocInfoMap),
		Metadata:  metadata,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := c.session.Query(`
		INSERT INTO clients (project_id, "_id", key, status, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		string(projectID), string(info.ID), key, database.ClientActivated, metadata, now, now,
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("insert client: %w", err)
	}

	refKey := types.ClientRefKey{ProjectID: projectID, ClientID: info.ID}
	c.clientCache.Add(refKey, info.DeepCopy())

	return info, nil
}

// TryAttaching updates the status of the document to Attaching to prevent
// deactivating the client while the document is being attached.
func (c *Client) TryAttaching(
	ctx context.Context,
	refKey types.ClientRefKey,
	docID types.ID,
) (*database.ClientInfo, error) {
	if !c.tables.Clients {
		return c.mongo.TryAttaching(ctx, refKey, docID)
	}

	info, err := c.readClientInfo(ctx, refKey.ProjectID, refKey.ClientID)
	if err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	if info.Status != database.ClientActivated {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, database.ErrClientNotFound)
	}
	if docInfo, ok := info.Documents[docID]; ok && docInfo.Status == database.DocumentAttached {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, database.ErrClientNotFound)
	}

	now := gotime.Now()

	// Insert into client_documents
	if err := c.session.Query(`
		INSERT INTO client_documents (project_id, client_id, doc_id, status, server_seq, client_seq)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(refKey.ProjectID), string(refKey.ClientID), string(docID),
		database.DocumentAttaching, int64(0), 0,
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	// Insert into doc_clients (reverse index)
	if err := c.session.Query(`
		INSERT INTO doc_clients (project_id, doc_id, client_id, doc_status)
		VALUES (?, ?, ?, ?)`,
		string(refKey.ProjectID), string(docID), string(refKey.ClientID),
		database.DocumentAttaching,
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	// Update client's updated_at
	if err := c.session.Query(`
		UPDATE clients SET updated_at = ?
		WHERE project_id = ? AND "_id" = ?`,
		now, string(refKey.ProjectID), string(refKey.ClientID),
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("try attaching %s to %s: %w", docID, refKey.ClientID, err)
	}

	info.Documents[docID] = &database.ClientDocInfo{
		Status:    database.DocumentAttaching,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	info.UpdatedAt = now

	c.clientCache.Add(refKey, info.DeepCopy())
	return info, nil
}

// DeactivateClient deactivates the client of the given refKey.
func (c *Client) DeactivateClient(
	ctx context.Context,
	refKey types.ClientRefKey,
) (*database.ClientInfo, error) {
	if !c.tables.Clients {
		return c.mongo.DeactivateClient(ctx, refKey)
	}

	now := gotime.Now()

	info, err := c.readClientInfo(ctx, refKey.ProjectID, refKey.ClientID)
	if err != nil {
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, err)
	}

	if info.Status != database.ClientActivated {
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	// Ensure no documents are currently attaching or attached
	for _, docInfo := range info.Documents {
		if docInfo.Status == database.DocumentAttaching || docInfo.Status == database.DocumentAttached {
			return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, database.ErrClientNotFound)
		}
	}

	if err := c.session.Query(`
		UPDATE clients SET status = ?, updated_at = ?
		WHERE project_id = ? AND "_id" = ?`,
		database.ClientDeactivated, now,
		string(refKey.ProjectID), string(refKey.ClientID),
	).WithContext(ctx).Exec(); err != nil {
		return nil, fmt.Errorf("deactivate client of %s: %w", refKey.ClientID, err)
	}

	info.Status = database.ClientDeactivated
	info.UpdatedAt = now

	c.clientCache.Add(refKey, info.DeepCopy())
	return info, nil
}

// FindClientInfoByRefKey finds the client of the given refKey.
func (c *Client) FindClientInfoByRefKey(
	ctx context.Context,
	refKey types.ClientRefKey,
	skipCache ...bool,
) (*database.ClientInfo, error) {
	if !c.tables.Clients {
		return c.mongo.FindClientInfoByRefKey(ctx, refKey, skipCache...)
	}

	skip := len(skipCache) > 0 && skipCache[0]

	if !skip {
		if cached, ok := c.clientCache.Get(refKey); ok {
			return cached.DeepCopy(), nil
		}
	}

	info, err := c.readClientInfo(ctx, refKey.ProjectID, refKey.ClientID)
	if err != nil {
		return nil, err
	}

	if !skip {
		c.clientCache.Add(refKey, info.DeepCopy())
	}
	return info, nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (c *Client) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	info *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	if !c.tables.Clients {
		return c.mongo.UpdateClientInfoAfterPushPull(ctx, info, docInfo)
	}

	clientKey := types.ClientRefKey{ProjectID: info.ProjectID, ClientID: info.ID}
	clientDocInfo, ok := info.Documents[docInfo.ID]
	if !ok {
		return fmt.Errorf(
			"update client of %s after PP %s: %w",
			info.ID, docInfo.ID, database.ErrDocumentNeverAttached,
		)
	}

	if existing, ok := c.clientCache.Get(clientKey); ok {
		if existingDocInfo, ok := existing.Documents[docInfo.ID]; ok {
			if existingDocInfo.ServerSeq >= clientDocInfo.ServerSeq &&
				existingDocInfo.ClientSeq >= clientDocInfo.ClientSeq &&
				existingDocInfo.Status == clientDocInfo.Status {
				return nil
			}
		}
	}

	c.clientCache.Remove(clientKey)

	attached, err := info.IsAttached(docInfo.ID)
	if err != nil {
		return err
	}

	// NOTE(hackerwins): Clear presence changes of the given client on the
	// document if the client is no longer attached to the document.
	docKey := types.DocRefKey{ProjectID: info.ProjectID, DocID: docInfo.ID}
	if prStore, ok := c.presenceCache.Get(docKey); ok && !attached {
		prStore.RemoveChangesByActor(info.ID)
	}

	// NOTE(scylla-poc): ScyllaDB's UPDATE/INSERT are upserts that silently
	// create rows even if the client doesn't exist. We must verify the client
	// exists first to match MongoDB's FindOneAndUpdate behavior.
	if _, err := c.readClientInfo(ctx, info.ProjectID, info.ID); err != nil {
		return fmt.Errorf("update client of %s after PP %s: %w", info.ID, docInfo.ID, database.ErrClientNotFound)
	}

	var sseq int64
	var cseq int
	if attached {
		sseq = clientDocInfo.ServerSeq
		cseq = int(clientDocInfo.ClientSeq)
	}

	// Update client_documents
	if err := c.session.Query(`
		INSERT INTO client_documents (project_id, client_id, doc_id, status, server_seq, client_seq)
		VALUES (?, ?, ?, ?, ?, ?)`,
		string(info.ProjectID), string(info.ID), string(docInfo.ID),
		clientDocInfo.Status, sseq, cseq,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("update client %s after PP %s: %w", info.ID, docInfo.ID, err)
	}

	// Update doc_clients (reverse index)
	if err := c.session.Query(`
		INSERT INTO doc_clients (project_id, doc_id, client_id, doc_status)
		VALUES (?, ?, ?, ?)`,
		string(info.ProjectID), string(docInfo.ID), string(info.ID),
		clientDocInfo.Status,
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("update doc_clients %s after PP %s: %w", info.ID, docInfo.ID, err)
	}

	// Update client's updated_at
	if err := c.session.Query(`
		UPDATE clients SET updated_at = ?
		WHERE project_id = ? AND "_id" = ?`,
		info.UpdatedAt, string(info.ProjectID), string(info.ID),
	).WithContext(ctx).Exec(); err != nil {
		return fmt.Errorf("update client %s updated_at after PP %s: %w", info.ID, docInfo.ID, err)
	}

	// Read back updated info and cache it
	updated, err := c.readClientInfo(ctx, info.ProjectID, info.ID)
	if err != nil {
		return fmt.Errorf("read back client %s after PP %s: %w", info.ID, docInfo.ID, err)
	}

	c.clientCache.Add(clientKey, updated.DeepCopy())
	return nil
}

// FindAttachedClientInfosByRefKey returns the attached client infos of the given document.
func (c *Client) FindAttachedClientInfosByRefKey(
	ctx context.Context,
	docRefKey types.DocRefKey,
) ([]*database.ClientInfo, error) {
	if !c.tables.Clients {
		return c.mongo.FindAttachedClientInfosByRefKey(ctx, docRefKey)
	}

	// Query doc_clients reverse index to find clients for this document
	iter := c.session.Query(`
		SELECT client_id, doc_status FROM doc_clients
		WHERE project_id = ? AND doc_id = ?`,
		string(docRefKey.ProjectID), string(docRefKey.DocID),
	).WithContext(ctx).Iter()

	var clientIDs []types.ID
	var clientID, docStatus string
	for iter.Scan(&clientID, &docStatus) {
		if docStatus == database.DocumentAttached {
			clientIDs = append(clientIDs, types.ID(clientID))
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("find attached clients of %s: %w", docRefKey, err)
	}

	var infos []*database.ClientInfo
	for _, cid := range clientIDs {
		info, err := c.readClientInfo(ctx, docRefKey.ProjectID, cid)
		if err != nil {
			continue
		}
		if info.Status == database.ClientActivated {
			infos = append(infos, info)
			refKey := types.ClientRefKey{ProjectID: info.ProjectID, ClientID: info.ID}
			c.clientCache.Add(refKey, info.DeepCopy())
		}
	}

	return infos, nil
}

// FindActiveClients finds active clients for deactivation checking.
func (c *Client) FindActiveClients(
	ctx context.Context,
	candidatesLimit int,
	lastClientID types.ID,
) ([]*database.ClientInfo, types.ID, error) {
	if !c.tables.Clients {
		return c.mongo.FindActiveClients(ctx, candidatesLimit, lastClientID)
	}

	// NOTE(scylla-poc): This uses ALLOW FILTERING for cross-partition scan.
	// In production, consider a dedicated table for active client lookups.
	iter := c.session.Query(`
		SELECT project_id, "_id" FROM clients
		WHERE status = ? ALLOW FILTERING`,
		database.ClientActivated,
	).WithContext(ctx).Iter()

	type clientRef struct {
		projectID types.ID
		clientID  types.ID
	}
	var candidates []clientRef
	var projID, id string
	for iter.Scan(&projID, &id) {
		cid := types.ID(id)
		if cid > lastClientID {
			candidates = append(candidates, clientRef{types.ID(projID), cid})
		}
	}
	if err := iter.Close(); err != nil {
		return nil, database.ZeroID, fmt.Errorf("find active clients: %w", err)
	}

	// Sort by clientID for consistent paging
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].clientID < candidates[j].clientID
	})

	if len(candidates) > candidatesLimit {
		candidates = candidates[:candidatesLimit]
	}

	var infos []*database.ClientInfo
	for _, ref := range candidates {
		info, err := c.readClientInfo(ctx, ref.projectID, ref.clientID)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}

	var lastID types.ID = database.ZeroID
	if len(infos) > 0 {
		lastID = infos[len(infos)-1].ID
	}

	return infos, lastID, nil
}

// FindAttachedClientCountsByDocIDs returns the number of attached clients of the given documents as a map.
func (c *Client) FindAttachedClientCountsByDocIDs(
	ctx context.Context,
	projectID types.ID,
	docIDs []types.ID,
) (map[types.ID]int, error) {
	if !c.tables.Clients {
		return c.mongo.FindAttachedClientCountsByDocIDs(ctx, projectID, docIDs)
	}

	if len(docIDs) == 0 {
		return map[types.ID]int{}, nil
	}

	attachedClientMap := make(map[types.ID]int)
	for _, docID := range docIDs {
		attachedClientMap[docID] = 0
	}

	for _, docID := range docIDs {
		iter := c.session.Query(`
			SELECT client_id, doc_status FROM doc_clients
			WHERE project_id = ? AND doc_id = ?`,
			string(projectID), string(docID),
		).WithContext(ctx).Iter()

		var clientID, docStatus string
		for iter.Scan(&clientID, &docStatus) {
			if docStatus == database.DocumentAttached {
				attachedClientMap[docID]++
			}
		}
		if err := iter.Close(); err != nil {
			return nil, fmt.Errorf("count attached clients of %s: %w", docID, err)
		}
	}

	return attachedClientMap, nil
}

// CountActivatedClients returns the number of activated clients in the given
// project. Overrides the mongo delegation so the count is read from ScyllaDB
// where the per-client rows live.
func (c *Client) CountActivatedClients(ctx context.Context, projectID types.ID) (int64, error) {
	if !c.tables.Clients {
		return c.mongo.CountActivatedClients(ctx, projectID)
	}

	var count int64
	if err := c.session.Query(`
		SELECT COUNT(*) FROM clients
		WHERE project_id = ? AND status = ? ALLOW FILTERING`,
		string(projectID), database.ClientActivated,
	).WithContext(ctx).Scan(&count); err != nil {
		return 0, fmt.Errorf("count clients of %s: %w", projectID, err)
	}
	return count, nil
}

// IsDocumentAttachedOrAttaching returns whether the given document is attached or attaching to clients.
func (c *Client) IsDocumentAttachedOrAttaching(
	ctx context.Context,
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	if !c.tables.Clients {
		return c.mongo.IsDocumentAttachedOrAttaching(ctx, docRefKey, excludeClientID)
	}

	iter := c.session.Query(`
		SELECT client_id, doc_status FROM doc_clients
		WHERE project_id = ? AND doc_id = ?`,
		string(docRefKey.ProjectID), string(docRefKey.DocID),
	).WithContext(ctx).Iter()

	var clientID, docStatus string
	for iter.Scan(&clientID, &docStatus) {
		if types.ID(clientID) == excludeClientID {
			continue
		}
		if docStatus == database.DocumentAttached || docStatus == database.DocumentAttaching {
			_ = iter.Close()
			return true, nil
		}
	}
	if err := iter.Close(); err != nil {
		return false, fmt.Errorf("is document %s attached: %w", docRefKey, err)
	}
	return false, nil
}
