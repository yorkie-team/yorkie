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

package memory

import (
	"context"
	"fmt"
	gotime "time"

	"github.com/hashicorp/go-memdb"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/db"
)

// DB is an in-memory database for testing or temporarily.
type DB struct {
	db *memdb.MemDB
}

// New returns a new in-memory database.
func New() (*DB, error) {
	memDB, err := memdb.NewMemDB(schema)
	if err != nil {
		return nil, err
	}

	return &DB{
		db: memDB,
	}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return nil
}

// FindProjectInfoByPublicKey returns a project by public key.
func (d *DB) FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*db.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "public_key", publicKey)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", publicKey, db.ErrProjectNotFound)
	}

	return raw.(*db.ProjectInfo).DeepCopy(), nil
}

// EnsureDefaultProjectInfo creates the default project if it does not exist.
func (d *DB) EnsureDefaultProjectInfo(ctx context.Context) (*db.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", db.DefaultProjectID.String())
	if err != nil {
		return nil, err
	}

	var info *db.ProjectInfo
	if raw == nil {
		info = db.NewProjectInfo(db.DefaultProjectName)
		info.ID = db.DefaultProjectID
		if err := txn.Insert(tblProjects, info); err != nil {
			return nil, err
		}
	} else {
		info = raw.(*db.ProjectInfo).DeepCopy()
	}

	txn.Commit()
	return info, nil
}

// CreateProjectInfo creates a new project.
func (d *DB) CreateProjectInfo(ctx context.Context, name string) (*db.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	info := db.NewProjectInfo(name)
	info.ID = newID()
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, err
	}
	txn.Commit()

	return info, nil
}

// ListProjectInfos returns all projects.
func (d *DB) ListProjectInfos(ctx context.Context) ([]*db.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblProjects, "id")
	if err != nil {
		return nil, err
	}

	var infos []*db.ProjectInfo
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		infos = append(infos, raw.(*db.ProjectInfo).DeepCopy())
	}

	return infos, nil
}

// UpdateProjectInfo updates the given project.
func (d *DB) UpdateProjectInfo(ctx context.Context, info *db.ProjectInfo) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert(tblProjects, info); err != nil {
		return err
	}
	txn.Commit()

	return nil
}

// ActivateClient activates a client.
func (d *DB) ActivateClient(ctx context.Context, key string) (*db.ClientInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "key", key)
	if err != nil {
		return nil, err
	}

	now := gotime.Now()

	clientInfo := &db.ClientInfo{
		Key:       key,
		Status:    db.ClientActivated,
		UpdatedAt: now,
	}

	if raw == nil {
		clientInfo.ID = newID()
		clientInfo.CreatedAt = now
	} else {
		loaded := raw.(*db.ClientInfo)
		clientInfo.ID = loaded.ID
		clientInfo.CreatedAt = loaded.CreatedAt
	}

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, err
	}

	txn.Commit()
	return clientInfo, nil
}

// DeactivateClient deactivates a client.
func (d *DB) DeactivateClient(ctx context.Context, clientID types.ID) (*db.ClientInfo, error) {
	if err := clientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientID.String())
	if err != nil {
		return nil, err
	}

	if raw == nil {
		return nil, fmt.Errorf("%s: %w", clientID, db.ErrClientNotFound)
	}

	// NOTE(hackerwins): When retrieving objects from go-memdb, references to
	// the stored objects are returned instead of new objects. This can cause
	// problems when directly modifying loaded objects. So, we need to DeepCopy.
	clientInfo := raw.(*db.ClientInfo).DeepCopy()
	clientInfo.Status = db.ClientDeactivated
	clientInfo.UpdatedAt = gotime.Now()

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, err
	}

	txn.Commit()
	return clientInfo, nil
}

// FindClientInfoByID finds a client by ID.
func (d *DB) FindClientInfoByID(ctx context.Context, clientID types.ID) (*db.ClientInfo, error) {
	if err := clientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientID.String())
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", clientID, db.ErrClientNotFound)
	}

	return raw.(*db.ClientInfo).DeepCopy(), nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (d *DB) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
) error {
	clientDocInfo := clientInfo.Documents[docInfo.ID]
	attached, err := clientInfo.IsAttached(docInfo.ID)
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientInfo.ID.String())
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", clientInfo.ID, db.ErrClientNotFound)
	}

	loaded := raw.(*db.ClientInfo).DeepCopy()

	if !attached {
		loaded.Documents[docInfo.ID] = &db.ClientDocInfo{
			Status: clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	} else {
		if _, ok := loaded.Documents[docInfo.ID]; !ok {
			loaded.Documents[docInfo.ID] = &db.ClientDocInfo{}
		}

		loadedClientDocInfo := loaded.Documents[docInfo.ID]
		serverSeq := loadedClientDocInfo.ServerSeq
		if clientDocInfo.ServerSeq > loadedClientDocInfo.ServerSeq {
			serverSeq = clientDocInfo.ServerSeq
		}
		clientSeq := loadedClientDocInfo.ClientSeq
		if clientDocInfo.ClientSeq > loadedClientDocInfo.ClientSeq {
			clientSeq = clientDocInfo.ClientSeq
		}
		loaded.Documents[docInfo.ID] = &db.ClientDocInfo{
			ServerSeq: serverSeq,
			ClientSeq: clientSeq,
			Status:    clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	}

	if err := txn.Insert(tblClients, loaded); err != nil {
		return err
	}
	txn.Commit()

	return nil
}

// FindDeactivateCandidates finds the clients that need housekeeping.
func (d *DB) FindDeactivateCandidates(
	ctx context.Context,
	inactiveThreshold gotime.Duration,
	candidatesLimit int,
) ([]*db.ClientInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	offset := gotime.Now().Add(-inactiveThreshold)

	var infos []*db.ClientInfo
	iterator, err := txn.ReverseLowerBound(
		tblClients,
		"status_updated_at",
		db.ClientActivated,
		offset,
	)
	if err != nil {
		return nil, err
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*db.ClientInfo)

		if info.Status != db.ClientActivated ||
			candidatesLimit <= len(infos) ||
			info.UpdatedAt.After(offset) {
			break
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// FindDocInfoByKey finds a docInfo by key.
func (d *DB) FindDocInfoByKey(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	key key.Key,
	createDocIfNotExist bool,
) (*db.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "key", key.String())
	if err != nil {
		return nil, err
	}
	if !createDocIfNotExist && raw == nil {
		return nil, fmt.Errorf("%s: %w", key, db.ErrDocumentNotFound)
	}

	now := gotime.Now()
	var docInfo *db.DocInfo
	if raw == nil {
		docInfo = &db.DocInfo{
			ID:         newID(),
			Key:        key,
			Owner:      clientInfo.ID,
			ServerSeq:  0,
			CreatedAt:  now,
			AccessedAt: now,
		}
		if err := txn.Insert(tblDocuments, docInfo); err != nil {
			return nil, err
		}
		txn.Commit()
	} else {
		docInfo = raw.(*db.DocInfo)
	}

	return docInfo.DeepCopy(), nil
}

// CreateChangeInfos stores the given changes and doc info.
func (d *DB) CreateChangeInfos(
	ctx context.Context,
	docInfo *db.DocInfo,
	initialServerSeq uint64,
	changes []*change.Change,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	for _, cn := range changes {
		encodedOperations, err := db.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		if err := txn.Insert(tblChanges, &db.ChangeInfo{
			ID:         newID(),
			DocID:      docInfo.ID,
			ServerSeq:  cn.ServerSeq(),
			ActorID:    types.ID(cn.ID().ActorID().String()),
			ClientSeq:  cn.ClientSeq(),
			Lamport:    cn.ID().Lamport(),
			Message:    cn.Message(),
			Operations: encodedOperations,
		}); err != nil {
			return err
		}
	}

	raw, err := txn.First(tblDocuments, "key", docInfo.Key.String())
	if err != nil {
		return err
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", docInfo.Key, db.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*db.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != initialServerSeq {
		return fmt.Errorf("%s: %w", docInfo.ID, db.ErrConflictOnUpdate)
	}

	loadedDocInfo.ServerSeq = docInfo.ServerSeq
	loadedDocInfo.UpdatedAt = gotime.Now()
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return err
	}

	txn.Commit()
	return nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (d *DB) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docID types.ID,
	from uint64,
	to uint64,
) ([]*change.Change, error) {
	infos, err := d.FindChangeInfosBetweenServerSeqs(ctx, docID, from, to)
	if err != nil {
		return nil, err
	}

	var changes []*change.Change
	for _, info := range infos {
		c, err := info.ToChange()
		if err != nil {
			return nil, err
		}
		changes = append(changes, c)
	}

	return changes, nil
}

// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
func (d *DB) FindChangeInfosBetweenServerSeqs(
	ctx context.Context,
	docID types.ID,
	from uint64,
	to uint64,
) ([]*db.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*db.ChangeInfo

	iterator, err := txn.LowerBound(
		tblChanges,
		"doc_id_server_seq",
		docID.String(),
		from,
	)
	if err != nil {
		return nil, err
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*db.ChangeInfo)
		if info.DocID != docID || info.ServerSeq > to {
			break
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (d *DB) CreateSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	doc *document.InternalDocument,
) error {
	snapshot, err := converter.ObjectToBytes(doc.RootObject())
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert(tblSnapshots, &db.SnapshotInfo{
		ID:        newID(),
		DocID:     docID,
		ServerSeq: doc.Checkpoint().ServerSeq,
		Lamport:   doc.Lamport(),
		Snapshot:  snapshot,
		CreatedAt: gotime.Now(),
	}); err != nil {
		return err
	}
	txn.Commit()
	return nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (d *DB) FindClosestSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	serverSeq uint64,
) (*db.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblSnapshots,
		"doc_id_server_seq",
		docID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, err
	}

	var snapshotInfo *db.SnapshotInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*db.SnapshotInfo)
		if info.DocID == docID {
			snapshotInfo = info
			break
		}
	}

	if snapshotInfo == nil {
		return &db.SnapshotInfo{}, nil
	}

	return snapshotInfo, nil
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
// and returns the min synced ticket.
func (d *DB) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docID types.ID,
	serverSeq uint64,
) (*time.Ticket, error) {
	if err := d.UpdateSyncedSeq(ctx, clientInfo, docID, serverSeq); err != nil {
		return nil, err
	}

	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.LowerBound(
		tblSyncedSeqs,
		"doc_id_lamport_actor_id",
		docID.String(),
		uint64(0),
		time.InitialActorID.String(),
	)
	if err != nil {
		return nil, err
	}

	var syncedSeqInfo *db.SyncedSeqInfo
	if raw := iterator.Next(); raw != nil {
		info := raw.(*db.SyncedSeqInfo)
		if info.DocID == docID {
			syncedSeqInfo = info
		}
	}

	if syncedSeqInfo == nil || syncedSeqInfo.ServerSeq == change.InitialServerSeq {
		return time.InitialTicket, nil
	}

	actorID, err := time.ActorIDFromHex(syncedSeqInfo.ActorID.String())
	if err != nil {
		return nil, err
	}

	return time.NewTicket(
		syncedSeqInfo.Lamport,
		time.MaxDelimiter,
		actorID,
	), nil
}

// UpdateSyncedSeq updates the syncedSeq of the given client.
func (d *DB) UpdateSyncedSeq(
	ctx context.Context,
	clientInfo *db.ClientInfo,
	docID types.ID,
	serverSeq uint64,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	isAttached, err := clientInfo.IsAttached(docID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = txn.DeleteAll(
			tblSyncedSeqs,
			"doc_id_client_id",
			docID.String(),
			clientInfo.ID.String(),
		); err != nil {
			return err
		}
		txn.Commit()
		return nil
	}

	ticket, err := d.findTicketByServerSeq(txn, docID, serverSeq)
	if err != nil {
		return err
	}

	raw, err := txn.First(
		tblSyncedSeqs,
		"doc_id_client_id",
		docID.String(),
		clientInfo.ID.String(),
	)
	if err != nil {
		return err
	}

	syncedSeqInfo := &db.SyncedSeqInfo{
		DocID:     docID,
		ClientID:  clientInfo.ID,
		Lamport:   ticket.Lamport(),
		ActorID:   types.ID(ticket.ActorID().String()),
		ServerSeq: serverSeq,
	}
	if raw == nil {
		syncedSeqInfo.ID = newID()
	} else {
		syncedSeqInfo.ID = raw.(*db.SyncedSeqInfo).ID
	}

	if err := txn.Insert(tblSyncedSeqs, syncedSeqInfo); err != nil {
		return err
	}

	txn.Commit()
	return nil
}

// FindDocInfosByPreviousIDAndPageSize returns the docInfos of the given previousID and pageSize.
func (d *DB) FindDocInfosByPreviousIDAndPageSize(
	ctx context.Context,
	previousID types.ID,
	pageSize int,
	isForward bool,
) ([]*db.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var iterator memdb.ResultIterator
	var err error
	if isForward {
		iterator, err = txn.LowerBound(
			tblDocuments,
			"id",
			previousID.String(),
		)
	} else {
		if previousID == "" {
			iterator, err = txn.GetReverse(
				tblDocuments,
				"id",
			)
		} else {
			iterator, err = txn.ReverseLowerBound(
				tblDocuments,
				"id",
				previousID.String(),
			)
		}
	}

	if err != nil {
		return nil, err
	}

	var docInfos []*db.DocInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*db.DocInfo)
		if len(docInfos) >= pageSize {
			break
		}

		if info.ID != previousID {
			docInfos = append(docInfos, info)
		}
	}

	return docInfos, nil
}

func (d *DB) findTicketByServerSeq(
	txn *memdb.Txn,
	docID types.ID,
	serverSeq uint64,
) (*time.Ticket, error) {
	if serverSeq == change.InitialServerSeq {
		return time.InitialTicket, nil
	}

	raw, err := txn.First(
		tblChanges,
		"doc_id_server_seq",
		docID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, err
	}
	if raw == nil {
		return nil, fmt.Errorf(
			"docID %s, serverSeq %d: %w",
			docID.String(),
			serverSeq,
			db.ErrDocumentNotFound,
		)
	}

	changeInfo := raw.(*db.ChangeInfo)
	actorID, err := time.ActorIDFromHex(changeInfo.ActorID.String())
	if err != nil {
		return nil, err
	}

	return time.NewTicket(
		changeInfo.Lamport,
		time.MaxDelimiter,
		actorID,
	), nil
}

func newID() types.ID {
	return types.ID(primitive.NewObjectID().Hex())
}
