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
	"encoding/json"
	"fmt"

	"github.com/gocql/gocql"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
)

// CreateChangeInfos stores the given changes and doc info.
func (c *Client) CreateChangeInfos(
	ctx context.Context,
	refKey types.DocRefKey,
	checkpoint change.Checkpoint,
	changes []*database.ChangeInfo,
	isRemoved bool,
) (*database.DocInfo, change.Checkpoint, error) {
	if !c.tables.Changes {
		return c.mongo.CreateChangeInfos(ctx, refKey, checkpoint, changes, isRemoved)
	}

	// 01. Fetch the document info (from MongoDB).
	docInfo, err := c.mongo.FindDocInfoByRefKey(ctx, refKey)
	if err != nil {
		return nil, change.InitialCheckpoint, err
	}
	if len(changes) == 0 && !isRemoved {
		return docInfo, checkpoint, nil
	}

	initialServerSeq := docInfo.ServerSeq

	// 02. Separate presence-only changes and operation changes.
	hasOperations := false
	var prChanges []*database.ChangeInfo
	var opChanges []*database.ChangeInfo
	for _, cn := range changes {
		serverSeq := docInfo.IncreaseServerSeq()
		checkpoint = checkpoint.NextServerSeq(serverSeq)
		cn.ServerSeq = serverSeq
		checkpoint = checkpoint.SyncClientSeq(cn.ClientSeq)

		if cn.PresenceOnly() {
			prChanges = append(prChanges, cn)
			continue
		}

		if cn.HasOperations() {
			opChanges = append(opChanges, cn)
			hasOperations = true
		}
	}

	// 03. Store presence-only changes.
	if len(prChanges) > 0 {
		var prStore *mongo.ChangeStore
		if cached, ok := c.presenceCache.Get(refKey); ok {
			prStore = cached
		} else {
			prStore = mongo.NewChangeStore()
			c.presenceCache.Add(refKey, prStore)
		}
		prStore.ReplaceOrInsert(prChanges)
	}

	// 04. Store operation changes in ScyllaDB.
	if len(opChanges) > 0 {
		batch := c.session.Batch(gocql.UnloggedBatch)
		for _, ch := range opChanges {
			jsonOperations, _ := json.Marshal(ch.Operations)
			jsonPresence, _ := json.Marshal(ch.PresenceChange)
			batch.Query(`
	INSERT INTO changes (
		project_id,
		doc_id,
		server_seq,
		"_id",
		actor_id,
		client_seq,
		lamport,
		version_vector,
		message,
		operations,
		presence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				refKey.ProjectID,
				refKey.DocID,
				ch.ServerSeq,
				ch.ID,
				ch.ActorID,
				ch.ClientSeq,
				ch.Lamport,
				ToScyllaVersionVector(ch.VersionVector),
				ch.Message,
				jsonOperations,
				jsonPresence,
			)
		}

		if err := c.session.ExecuteBatch(batch); err != nil {
			return nil, change.InitialCheckpoint, fmt.Errorf("batch insert changes %v: %w", refKey, err)
		}
	}

	var opStore *mongo.ChangeStore
	if cached, ok := c.changeCache.Get(refKey); ok {
		opStore = cached
	} else {
		opStore = mongo.NewChangeStore()
		c.changeCache.Add(refKey, opStore)
	}
	opStore.ReplaceOrInsert(opChanges)
	opStore.ExpandRange(mongo.ChangeRange{From: initialServerSeq + 1, To: docInfo.ServerSeq})

	// 05. Update the document info in MongoDB. The helper refreshes mongo's
	// docCache with the mutated docInfo on success — required to close the
	// stale-cache window that lock-free readers (e.g., empty PushPullChanges
	// that reaches scylla.CreateChangeInfos through the database interface)
	// would otherwise leave open between Remove and UpdateOne.
	if err := c.mongo.UpdateDocAfterChanges(
		ctx, docInfo, initialServerSeq, hasOperations, isRemoved,
	); err != nil {
		return nil, change.InitialCheckpoint, err
	}

	return docInfo, checkpoint, nil
}

// CompactChangeInfos stores the given compacted changes then updates the docInfo.
func (c *Client) CompactChangeInfos(
	ctx context.Context,
	docInfo *database.DocInfo,
	lastServerSeq int64,
	changes []*change.Change,
) error {
	if !c.tables.Changes {
		return c.mongo.CompactChangeInfos(ctx, docInfo, lastServerSeq, changes)
	}

	// 1. Purge the resources of the document across both stores so the post-
	//    compaction state is clean regardless of where snapshots/VV live.
	if _, err := c.mongo.PurgeDocumentInternals(ctx, docInfo.ProjectID, docInfo.ID); err != nil {
		return err
	}
	if _, err := c.purgeDocumentInternals(ctx, docInfo.ProjectID, docInfo.ID); err != nil {
		return err
	}

	// 2. Store compacted change in ScyllaDB
	var newServerSeq int64 = 1
	if len(changes) == 0 {
		newServerSeq = 0
	} else if len(changes) != 1 {
		return fmt.Errorf("compact document of %s: invalid change size %d", docInfo.RefKey(), len(changes))
	}

	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		jsonOperations, _ := json.Marshal(encodedOperations)
		jsonPresence, _ := json.Marshal(cn.PresenceChange())
		if err := c.session.Query(`
	INSERT INTO changes (
		project_id,
		doc_id,
		server_seq,
		client_seq,
		lamport,
		actor_id,
		version_vector,
		message,
		operations,
		presence
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			docInfo.ProjectID,
			docInfo.ID,
			newServerSeq,
			cn.ClientSeq(),
			cn.ID().Lamport(),
			types.ID(cn.ID().ActorID().String()),
			ToScyllaVersionVector(cn.ID().VersionVector()),
			cn.Message(),
			jsonOperations,
			jsonPresence,
		).Exec(); err != nil {
			return fmt.Errorf("compact document of %s: %w", docInfo.RefKey(), err)
		}
	}

	// 3. Update document in MongoDB
	if err := c.mongo.CompactDocAfterChanges(
		ctx, docInfo.ProjectID, docInfo.ID, lastServerSeq, newServerSeq,
	); err != nil {
		return err
	}

	return nil
}

// FindLatestChangeInfoByActor returns the latest change created by given actorID.
func (c *Client) FindLatestChangeInfoByActor(
	ctx context.Context,
	docRefKey types.DocRefKey,
	actorID types.ID,
	serverSeq int64,
) (*database.ChangeInfo, error) {
	if !c.tables.Changes {
		return c.mongo.FindLatestChangeInfoByActor(ctx, docRefKey, actorID, serverSeq)
	}

	info := &database.ChangeInfo{}

	query := `
	SELECT
		project_id,
		doc_id,
		server_seq,
		client_seq,
		lamport,
		actor_id,
		version_vector,
		message,
		operations,
		presence
	FROM changes
	WHERE project_id = ? AND doc_id = ? AND actor_id = ? AND server_seq <= ?
	ORDER BY server_seq DESC
	LIMIT 1
	ALLOW FILTERING`

	var (
		projectIDStr string
		docIDStr     string
		serverSeqP   int64
		clientSeq    int32
		lamport      int64
		actorIDStr   string
		vvMap        map[string]int64
		message      string
		opsBlob      []byte
		prBlob       []byte
	)

	q := c.session.Query(
		query,
		docRefKey.ProjectID,
		docRefKey.DocID,
		actorID,
		serverSeq,
	).WithContext(ctx)
	iter := q.Iter()

	if iter.Scan(
		&projectIDStr,
		&docIDStr,
		&serverSeqP,
		&clientSeq,
		&lamport,
		&actorIDStr,
		&vvMap,
		&message,
		&opsBlob,
		&prBlob,
	) {
		var ops [][]byte
		if len(opsBlob) > 0 {
			if err := json.Unmarshal(opsBlob, &ops); err != nil {
				_ = iter.Close()
				return nil, fmt.Errorf("decode operations from scylla: %w", err)
			}
		}

		var prChange *presence.Change
		if len(prBlob) > 0 {
			var tmp presence.Change
			if err := json.Unmarshal(prBlob, &tmp); err != nil {
				_ = iter.Close()
				return nil, fmt.Errorf("decode presence from scylla: %w", err)
			}
			prChange = &tmp
		}

		info = &database.ChangeInfo{
			ProjectID:      types.ID(projectIDStr),
			DocID:          types.ID(docIDStr),
			ServerSeq:      serverSeqP,
			ClientSeq:      uint32(clientSeq),
			Lamport:        lamport,
			ActorID:        types.ID(actorIDStr),
			VersionVector:  FromScyllaVersionVector(vvMap),
			Message:        message,
			Operations:     ops,
			PresenceChange: prChange,
		}
	}
	if err := iter.Close(); err != nil {
		return nil, fmt.Errorf("find the latest change of %s: %w", docRefKey, err)
	}

	return info, nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (c *Client) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*change.Change, error) {
	if !c.tables.Changes {
		return c.mongo.FindChangesBetweenServerSeqs(ctx, docRefKey, from, to)
	}

	infos, err := c.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
	if err != nil {
		return nil, err
	}

	var result []*change.Change
	for _, info := range infos {
		ch, err := info.ToChange()
		if err != nil {
			return nil, err
		}
		result = append(result, ch)
	}

	return result, nil
}

// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
func (c *Client) FindChangeInfosBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	if !c.tables.Changes {
		return c.mongo.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
	}

	if from > to {
		return nil, nil
	}

	// 01. Create a temporary change store to hold the changes.
	store := mongo.NewChangeStore()

	// 02. Fill the store with presence only changes.
	if prStore, ok := c.presenceCache.Get(docRefKey); ok {
		store.ReplaceOrInsert(prStore.ChangesInRange(from, to))
	}

	// 03. Fill the store with operation changes. If not cached, load from DB.
	var opStore *mongo.ChangeStore
	if cached, ok := c.changeCache.Get(docRefKey); ok {
		opStore = cached
	} else {
		opStore = mongo.NewChangeStore()
		c.changeCache.Add(docRefKey, opStore)
	}

	if err := opStore.EnsureChanges(from, to, func(from, to int64) ([]*database.ChangeInfo, error) {
		const chunkSize int64 = 1000
		var infos []*database.ChangeInfo
		current := from
		for current <= to {
			query := `
	SELECT
		project_id,
		doc_id,
		server_seq,
		client_seq,
		lamport,
		actor_id,
		version_vector,
		message,
		operations,
		presence
	FROM changes
	WHERE project_id = ? AND doc_id = ? AND server_seq >= ? AND server_seq <= ?
	ORDER BY server_seq ASC
	LIMIT ?`

			iter := c.session.Query(
				query,
				docRefKey.ProjectID,
				docRefKey.DocID,
				current,
				to,
				chunkSize,
			).WithContext(ctx).Iter()

			var chunk []*database.ChangeInfo
			for {
				var (
					projectIDStr string
					docIDStr     string
					serverSeq    int64
					clientSeq    int32
					lamport      int64
					actorIDStr   string
					vvMap        map[string]int64
					message      string
					opsBlob      []byte
					prBlob       []byte
				)

				if !iter.Scan(
					&projectIDStr,
					&docIDStr,
					&serverSeq,
					&clientSeq,
					&lamport,
					&actorIDStr,
					&vvMap,
					&message,
					&opsBlob,
					&prBlob,
				) {
					break
				}

				var ops [][]byte
				if len(opsBlob) > 0 {
					if err := json.Unmarshal(opsBlob, &ops); err != nil {
						_ = iter.Close()
						return nil, fmt.Errorf("decode operations from scylla: %w", err)
					}
				}

				var prChange *presence.Change
				if len(prBlob) > 0 {
					var tmp presence.Change
					if err := json.Unmarshal(prBlob, &tmp); err != nil {
						_ = iter.Close()
						return nil, fmt.Errorf("decode presence from scylla: %w", err)
					}
					prChange = &tmp
				}

				chunk = append(chunk, &database.ChangeInfo{
					ProjectID:      types.ID(projectIDStr),
					DocID:          types.ID(docIDStr),
					ServerSeq:      serverSeq,
					ClientSeq:      uint32(clientSeq),
					Lamport:        lamport,
					ActorID:        types.ID(actorIDStr),
					VersionVector:  FromScyllaVersionVector(vvMap),
					Message:        message,
					Operations:     ops,
					PresenceChange: prChange,
				})
			}

			if err := iter.Close(); err != nil {
				return nil, fmt.Errorf("find changes of %s: %w", docRefKey, err)
			}

			if len(chunk) == 0 {
				break
			}
			infos = append(infos, chunk...)
			last := chunk[len(chunk)-1].ServerSeq
			if last >= to || int64(len(chunk)) < chunkSize {
				break
			}
			current = last + 1
		}
		return infos, nil
	}); err != nil {
		return nil, err
	}
	store.ReplaceOrInsert(opStore.ChangesInRange(from, to))

	// 04. Return the changes in the given range.
	return store.ChangesInRange(from, to), nil
}
