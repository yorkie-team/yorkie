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

// Package memory implements the database interface using in-memory database.
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
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// DB is an in-memory database for testing or temporarily.
type DB struct {
	db *memdb.MemDB
}

// New returns a new in-memory database.
func New() (*DB, error) {
	memDB, err := memdb.NewMemDB(schema)
	if err != nil {
		return nil, fmt.Errorf("new memdb: %w", err)
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
func (d *DB) FindProjectInfoByPublicKey(
	ctx context.Context,
	publicKey string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "public_key", publicKey)
	if err != nil {
		return nil, fmt.Errorf("find project by public key: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", publicKey, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// FindProjectInfoByName returns a project by the given name.
func (d *DB) FindProjectInfoByName(
	ctx context.Context,
	owner types.ID,
	name string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "owner_name", owner.String(), name)
	if err != nil {
		return nil, fmt.Errorf("find project by owner and name: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", name, database.ErrProjectNotFound)
	}

	info := raw.(*database.ProjectInfo).DeepCopy()

	return info, nil
}

// FindProjectInfoByID returns a project by the given id.
func (d *DB) FindProjectInfoByID(ctx context.Context, id types.ID) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// EnsureDefaultUserAndProject creates the default user and project if they do not exist.
func (d *DB) EnsureDefaultUserAndProject(
	ctx context.Context,
	username,
	password string,
	clientDeactivateThreshold string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	user, err := d.ensureDefaultUserInfo(ctx, username, password)
	if err != nil {
		return nil, nil, err
	}

	project, err := d.ensureDefaultProjectInfo(ctx, user.ID, clientDeactivateThreshold)
	if err != nil {
		return nil, nil, err
	}

	return user, project, nil
}

// ensureDefaultUserInfo creates the default user if it does not exist.
func (d *DB) ensureDefaultUserInfo(
	ctx context.Context,
	username,
	password string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("find user by username: %w", err)
	}

	var info *database.UserInfo
	if raw == nil {
		hashedPassword, err := database.HashedPassword(password)
		if err != nil {
			return nil, err
		}
		info = database.NewUserInfo(username, hashedPassword)
		info.ID = newID()
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("insert user: %w", err)
		}
	} else {
		info = raw.(*database.UserInfo).DeepCopy()
	}

	txn.Commit()
	return info, nil
}

// ensureDefaultProjectInfo creates the default project if it does not exist.
func (d *DB) ensureDefaultProjectInfo(
	ctx context.Context,
	defaultUserID types.ID,
	defaultClientDeactivateThreshold string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", database.DefaultProjectID.String())
	if err != nil {
		return nil, fmt.Errorf("find default project: %w", err)
	}

	var info *database.ProjectInfo
	if raw == nil {
		info = database.NewProjectInfo(database.DefaultProjectName, defaultUserID, defaultClientDeactivateThreshold)
		info.ID = database.DefaultProjectID
		if err := txn.Insert(tblProjects, info); err != nil {
			return nil, fmt.Errorf("insert project: %w", err)
		}
	} else {
		info = raw.(*database.ProjectInfo).DeepCopy()
	}

	txn.Commit()
	return info, nil
}

// CreateProjectInfo creates a new project.
func (d *DB) CreateProjectInfo(
	ctx context.Context,
	name string,
	owner types.ID,
	clientDeactivateThreshold string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// NOTE(hackerwins): Check if the project already exists.
	// https://github.com/hashicorp/go-memdb/issues/7#issuecomment-270427642
	existing, err := txn.First(tblProjects, "owner_name", owner.String(), name)
	if err != nil {
		return nil, fmt.Errorf("find project by owner and name: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("%s: %w", name, database.ErrProjectAlreadyExists)
	}

	info := database.NewProjectInfo(name, owner, clientDeactivateThreshold)
	info.ID = newID()
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, fmt.Errorf("insert project: %w", err)
	}
	txn.Commit()

	return info, nil
}

// listProjectInfos returns all project infos rotationally.
func (d *DB) listProjectInfos(
	ctx context.Context,
	pageSize int,
	housekeepingLastProjectID types.ID,
) ([]*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(
		tblProjects,
		"id",
		housekeepingLastProjectID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetch projects: %w", err)
	}

	var infos []*database.ProjectInfo

	for i := 0; i < pageSize; i++ {
		raw := iter.Next()
		if raw == nil {
			break
		}
		info := raw.(*database.ProjectInfo).DeepCopy()

		if i == 0 && info.ID == housekeepingLastProjectID {
			pageSize++
			continue
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// ListProjectInfos returns all project infos owned by owner.
func (d *DB) ListProjectInfos(
	ctx context.Context,
	owner types.ID,
) ([]*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(
		tblProjects,
		"owner_name",
		owner.String(),
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("fetch projects by owner and name: %w", err)
	}

	var infos []*database.ProjectInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ProjectInfo).DeepCopy()
		if info.Owner != owner {
			break
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// UpdateProjectInfo updates the given project.
func (d *DB) UpdateProjectInfo(
	ctx context.Context,
	owner types.ID,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	info := raw.(*database.ProjectInfo).DeepCopy()
	if info.Owner != owner {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	if fields.Name != nil {
		existing, err := txn.First(tblProjects, "owner_name", owner.String(), *fields.Name)
		if err != nil {
			return nil, fmt.Errorf("find project by owner and name: %w", err)
		}
		if existing != nil && info.Name != *fields.Name {
			return nil, fmt.Errorf("%s: %w", *fields.Name, database.ErrProjectNameAlreadyExists)
		}
	}

	info.UpdateFields(fields)
	info.UpdatedAt = gotime.Now()
	if err := txn.Insert(tblProjects, info); err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}
	txn.Commit()

	return info, nil
}

// CreateUserInfo creates a new user.
func (d *DB) CreateUserInfo(
	ctx context.Context,
	username string,
	hashedPassword string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	existing, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("find user by username: %w", err)
	}
	if existing != nil {
		return nil, fmt.Errorf("%s: %w", username, database.ErrUserAlreadyExists)
	}

	info := database.NewUserInfo(username, hashedPassword)
	info.ID = newID()
	if err := txn.Insert(tblUsers, info); err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}
	txn.Commit()

	return info, nil
}

// FindUserInfo finds a user by the given username.
func (d *DB) FindUserInfo(ctx context.Context, username string) (*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return nil, fmt.Errorf("find user by username: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
	}

	return raw.(*database.UserInfo).DeepCopy(), nil
}

// ListUserInfos returns all users.
func (d *DB) ListUserInfos(
	ctx context.Context,
) ([]*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblUsers, "id")
	if err != nil {
		return nil, fmt.Errorf("fetch users: %w", err)
	}

	var infos []*database.UserInfo
	for {
		raw := iter.Next()
		if raw == nil {
			break
		}
		infos = append(infos, raw.(*database.UserInfo).DeepCopy())
	}

	return infos, nil
}

// ActivateClient activates a client.
func (d *DB) ActivateClient(
	ctx context.Context,
	projectID types.ID,
	key string,
) (*database.ClientInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "project_id_key", projectID.String(), key)
	if err != nil {
		return nil, fmt.Errorf("find client by project id and key: %w", err)
	}

	now := gotime.Now()

	clientInfo := &database.ClientInfo{
		ProjectID: projectID,
		Key:       key,
		Status:    database.ClientActivated,
		UpdatedAt: now,
	}

	if raw == nil {
		clientInfo.ID = newID()
		clientInfo.CreatedAt = now
	} else {
		loaded := raw.(*database.ClientInfo)
		clientInfo.ID = loaded.ID
		clientInfo.CreatedAt = loaded.CreatedAt
	}

	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, fmt.Errorf("insert client: %w", err)
	}

	txn.Commit()
	return clientInfo, nil
}

// DeactivateClient deactivates a client.
func (d *DB) DeactivateClient(ctx context.Context, projectID, clientID types.ID) (*database.ClientInfo, error) {
	if err := clientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientID.String())
	if err != nil {
		return nil, fmt.Errorf("find client by id: %w", err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%s: %w", clientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(projectID); err != nil {
		return nil, err
	}

	// NOTE(hackerwins): When retrieving objects from go-memdb, references to
	// the stored objects are returned instead of new objects. This can cause
	// problems when directly modifying loaded objects. So, we need to DeepCopy.
	clientInfo = clientInfo.DeepCopy()
	clientInfo.Deactivate()
	if err := txn.Insert(tblClients, clientInfo); err != nil {
		return nil, fmt.Errorf("update client: %w", err)
	}

	txn.Commit()
	return clientInfo, nil
}

// FindClientInfoByID finds a client by ID.
func (d *DB) FindClientInfoByID(ctx context.Context, projectID, clientID types.ID) (*database.ClientInfo, error) {
	if err := clientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", clientID.String())
	if err != nil {
		return nil, fmt.Errorf("find client by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", clientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(projectID); err != nil {
		return nil, err
	}

	return clientInfo.DeepCopy(), nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (d *DB) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
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
		return fmt.Errorf("find client by id: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", clientInfo.ID, database.ErrClientNotFound)
	}

	loaded := raw.(*database.ClientInfo).DeepCopy()

	if !attached {
		loaded.Documents[docInfo.ID] = &database.ClientDocInfo{
			Status: clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	} else {
		if _, ok := loaded.Documents[docInfo.ID]; !ok {
			loaded.Documents[docInfo.ID] = &database.ClientDocInfo{}
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
		loaded.Documents[docInfo.ID] = &database.ClientDocInfo{
			ServerSeq: serverSeq,
			ClientSeq: clientSeq,
			Status:    clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	}

	if err := txn.Insert(tblClients, loaded); err != nil {
		return fmt.Errorf("update client: %w", err)
	}
	txn.Commit()

	return nil
}

// findDeactivateCandidatesPerProject finds the clients that need housekeeping per project.
func (d *DB) findDeactivateCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
) ([]*database.ClientInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	clientDeactivateThreshold, err := project.ClientDeactivateThresholdAsTimeDuration()
	if err != nil {
		return nil, err
	}

	offset := gotime.Now().Add(-clientDeactivateThreshold)

	var infos []*database.ClientInfo
	iterator, err := txn.ReverseLowerBound(
		tblClients,
		"project_id_status_updated_at",
		project.ID.String(),
		database.ClientActivated,
		offset,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch deactivated clients: %w", err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ClientInfo)

		if info.Status != database.ClientActivated ||
			candidatesLimit <= len(infos) ||
			info.UpdatedAt.After(offset) {
			break
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// FindDeactivateCandidates finds the clients that need housekeeping.
func (d *DB) FindDeactivateCandidates(
	ctx context.Context,
	candidatesLimitPerProject int,
	projectFetchSize int,
	lastProjectID types.ID,
) (types.ID, []*database.ClientInfo, error) {
	projects, err := d.listProjectInfos(ctx, projectFetchSize, lastProjectID)
	if err != nil {
		return database.DefaultProjectID, nil, err
	}

	var candidates []*database.ClientInfo
	for _, project := range projects {
		infos, err := d.findDeactivateCandidatesPerProject(ctx, project, candidatesLimitPerProject)
		if err != nil {
			return database.DefaultProjectID, nil, err
		}

		candidates = append(candidates, infos...)
	}

	var topProjectID types.ID
	if len(projects) < projectFetchSize {
		topProjectID = database.DefaultProjectID
	} else {
		topProjectID = projects[len(projects)-1].ID
	}

	return topProjectID, candidates, nil
}

// FindDocInfoByKeyAndOwner finds the document of the given key. If the
// createDocIfNotExist condition is true, create the document if it does not
// exist.
func (d *DB) FindDocInfoByKeyAndOwner(
	ctx context.Context,
	projectID types.ID,
	clientID types.ID,
	key key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// TODO(hackerwins): Removed documents should be filtered out by the query, but
	// somehow it does not work. This is a workaround.
	// val, err := txn.First(tblDocuments, "project_id_key_removed_at", projectID.String(), key.String(), gotime.Time{})
	iter, err := txn.Get(tblDocuments, "project_id_key_removed_at", projectID.String(), key.String(), gotime.Time{})
	if err != nil {
		return nil, fmt.Errorf("find document by key: %w", err)
	}
	var raw interface{}
	for val := iter.Next(); val != nil; val = iter.Next() {
		if val != nil && val.(*database.DocInfo).RemovedAt.IsZero() {
			raw = val
		}
	}

	if err != nil {
		return nil, fmt.Errorf("find document by key: %w", err)
	}
	if !createDocIfNotExist && raw == nil {
		raw, err = txn.First(tblDocuments, "project_id_key", projectID.String(), key.String())
		if err != nil {
			return nil, fmt.Errorf("find document by key: %w", err)
		}
		if raw == nil {
			return nil, fmt.Errorf("%s: %w", key, database.ErrDocumentNotFound)
		}
	}

	now := gotime.Now()
	var docInfo *database.DocInfo
	if raw == nil {
		docInfo = &database.DocInfo{
			ID:         newID(),
			ProjectID:  projectID,
			Key:        key,
			Owner:      clientID,
			ServerSeq:  0,
			CreatedAt:  now,
			AccessedAt: now,
		}
		if err := txn.Insert(tblDocuments, docInfo); err != nil {
			return nil, fmt.Errorf("create document: %w", err)
		}
		txn.Commit()
	} else {
		docInfo = raw.(*database.DocInfo)
	}

	return docInfo.DeepCopy(), nil
}

// FindDocInfoByKey finds the document of the given key.
func (d *DB) FindDocInfoByKey(
	ctx context.Context,
	projectID types.ID,
	key key.Key,
) (*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "project_id_key_removed_at", projectID.String(), key.String(), gotime.Time{})
	if err != nil {
		return nil, fmt.Errorf("find document by key: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", key, database.ErrDocumentNotFound)
	}

	return raw.(*database.DocInfo).DeepCopy(), nil
}

// FindDocInfoByID finds a docInfo of the given ID.
func (d *DB) FindDocInfoByID(
	ctx context.Context,
	projectID types.ID,
	id types.ID,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find document by id: %w", err)
	}

	if raw == nil {
		return nil, fmt.Errorf("finding doc info by ID(%s): %w", id, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != projectID {
		return nil, fmt.Errorf("finding doc info by ID(%s): %w", id, database.ErrDocumentNotFound)
	}

	return docInfo.DeepCopy(), nil
}

// UpdateDocInfoStatusToRemoved updates the status of the document to removed.
func (d *DB) UpdateDocInfoStatusToRemoved(
	ctx context.Context,
	projectID types.ID,
	id types.ID,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", id.String())
	if err != nil {
		return fmt.Errorf("find document by id: %w", err)
	}

	if raw == nil {
		return fmt.Errorf("finding doc info by ID(%s): %w", id, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != projectID {
		return fmt.Errorf("finding doc info by ID(%s): %w", id, database.ErrDocumentNotFound)
	}

	docInfo.RemovedAt = gotime.Now()

	if err := txn.Delete(tblDocuments, docInfo); err != nil {
		return fmt.Errorf("delete document: %w", err)
	}
	if err := txn.Insert(tblDocuments, docInfo); err != nil {
		return fmt.Errorf("insert document: %w", err)
	}

	txn.Commit()

	return nil
}

// CreateChangeInfos stores the given changes and doc info. If the
// removeDoc condition is true, mark IsRemoved to true in doc info.
func (d *DB) CreateChangeInfos(
	ctx context.Context,
	projectID types.ID,
	docInfo *database.DocInfo,
	initialServerSeq int64,
	changes []*change.Change,
	isRemoved bool,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}
		encodedPresence, err := database.EncodePresenceChange(cn.PresenceChange())
		if err != nil {
			return err
		}

		if err := txn.Insert(tblChanges, &database.ChangeInfo{
			ID:             newID(),
			DocID:          docInfo.ID,
			ServerSeq:      cn.ServerSeq(),
			ActorID:        types.ID(cn.ID().ActorID().String()),
			ClientSeq:      cn.ClientSeq(),
			Lamport:        cn.ID().Lamport(),
			Message:        cn.Message(),
			Operations:     encodedOperations,
			PresenceChange: encodedPresence,
		}); err != nil {
			return fmt.Errorf("create change: %w", err)
		}
	}

	raw, err := txn.First(
		tblDocuments,
		"project_id_id",
		projectID.String(),
		docInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("find document: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*database.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != initialServerSeq {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrConflictOnUpdate)
	}

	now := gotime.Now()
	loadedDocInfo.ServerSeq = docInfo.ServerSeq
	loadedDocInfo.UpdatedAt = now
	if isRemoved {
		loadedDocInfo.RemovedAt = now
	}
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return fmt.Errorf("update document: %w", err)
	}
	txn.Commit()

	if isRemoved {
		docInfo.RemovedAt = now
	}

	return nil
}

// PurgeStaleChanges delete changes before the smallest in `syncedseqs` to
// save storage.
func (d *DB) PurgeStaleChanges(
	ctx context.Context,
	docID types.ID,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// Find the smallest server seq in `syncedseqs`.
	// Because offline client can pull changes when it becomes online.
	it, err := txn.Get(tblSyncedSeqs, "id")
	if err != nil {
		return fmt.Errorf("fetch syncedseqs: %w", err)
	}

	minSyncedServerSeq := change.MaxServerSeq
	for raw := it.Next(); raw != nil; raw = it.Next() {
		info := raw.(*database.SyncedSeqInfo)
		if info.DocID == docID && info.ServerSeq < minSyncedServerSeq {
			minSyncedServerSeq = info.ServerSeq
		}
	}
	if minSyncedServerSeq == change.MaxServerSeq {
		return nil
	}

	// Delete all changes before the smallest server seq.
	iterator, err := txn.ReverseLowerBound(
		tblChanges,
		"doc_id_server_seq",
		docID.String(),
		minSyncedServerSeq,
	)
	if err != nil {
		return fmt.Errorf("fetch changes before %d: %w", minSyncedServerSeq, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if err = txn.Delete(tblChanges, info); err != nil {
			return fmt.Errorf("delete change %s: %w", info.ID, err)
		}
	}
	return nil
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (d *DB) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docID types.ID,
	from int64,
	to int64,
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
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*database.ChangeInfo

	iterator, err := txn.LowerBound(
		tblChanges,
		"doc_id_server_seq",
		docID.String(),
		from,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch changes from %d: %w", from, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if info.DocID != docID || info.ServerSeq > to {
			break
		}
		infos = append(infos, info.DeepCopy())
	}
	return infos, nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (d *DB) CreateSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	doc *document.InternalDocument,
) error {
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert(tblSnapshots, &database.SnapshotInfo{
		ID:        newID(),
		DocID:     docID,
		ServerSeq: doc.Checkpoint().ServerSeq,
		Lamport:   doc.Lamport(),
		Snapshot:  snapshot,
		CreatedAt: gotime.Now(),
	}); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}
	txn.Commit()
	return nil
}

// FindSnapshotInfoByID returns the snapshot by the given id.
func (d *DB) FindSnapshotInfoByID(ctx context.Context, id types.ID) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	raw, err := txn.First(tblSnapshots, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find snapshot by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrSnapshotNotFound)
	}

	return raw.(*database.SnapshotInfo).DeepCopy(), nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (d *DB) FindClosestSnapshotInfo(
	ctx context.Context,
	docID types.ID,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblSnapshots,
		"doc_id_server_seq",
		docID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch snapshots before %d: %w", serverSeq, err)
	}

	var snapshotInfo *database.SnapshotInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.SnapshotInfo)
		if info.DocID == docID {
			snapshotInfo = &database.SnapshotInfo{
				ID:        info.ID,
				DocID:     info.DocID,
				ServerSeq: info.ServerSeq,
				Lamport:   info.Lamport,
				CreatedAt: info.CreatedAt,
			}
			if includeSnapshot {
				snapshotInfo.Snapshot = info.Snapshot
			}
			break
		}
	}

	if snapshotInfo == nil {
		return &database.SnapshotInfo{}, nil
	}

	return snapshotInfo, nil
}

// FindMinSyncedSeqInfo finds the minimum synced sequence info.
func (d *DB) FindMinSyncedSeqInfo(
	ctx context.Context,
	docID types.ID,
) (*database.SyncedSeqInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	it, err := txn.Get(tblSyncedSeqs, "id")
	if err != nil {
		return nil, fmt.Errorf("fetch syncedseqs: %w", err)
	}

	syncedSeqInfo := &database.SyncedSeqInfo{}
	minSyncedServerSeq := change.MaxServerSeq
	for raw := it.Next(); raw != nil; raw = it.Next() {
		info := raw.(*database.SyncedSeqInfo)
		if info.DocID == docID && info.ServerSeq < minSyncedServerSeq {
			minSyncedServerSeq = info.ServerSeq
			syncedSeqInfo = info
		}
	}
	if minSyncedServerSeq == change.MaxServerSeq {
		return nil, nil
	}

	return syncedSeqInfo, nil
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
// and returns the min synced ticket.
func (d *DB) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq int64,
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
		int64(0),
		time.InitialActorID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetch smallest syncedseq of %s: %w", docID.String(), err)
	}

	var syncedSeqInfo *database.SyncedSeqInfo
	if raw := iterator.Next(); raw != nil {
		info := raw.(*database.SyncedSeqInfo)
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
	clientInfo *database.ClientInfo,
	docID types.ID,
	serverSeq int64,
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
			return fmt.Errorf("delete syncedseqs of %s: %w", docID.String(), err)
		}
		txn.Commit()
		return nil
	}

	ticket, err := d.findTicketByServerSeq(txn, docID, serverSeq)
	if err != nil {
		return err
	}

	// NOTE: skip storing the initial ticket to prevent GC interruption.
	//       Documents in this state do not need to be saved because they do not
	//       have any tombstones to be referenced by other documents.
	//
	//       (The initial ticket is used as the creation time of the root
	//       element that operations can not remove.)
	if ticket.Compare(time.InitialTicket) == 0 {
		return nil
	}

	raw, err := txn.First(
		tblSyncedSeqs,
		"doc_id_client_id",
		docID.String(),
		clientInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("fetch syncedseqs of %s: %w", docID.String(), err)
	}

	syncedSeqInfo := &database.SyncedSeqInfo{
		DocID:     docID,
		ClientID:  clientInfo.ID,
		Lamport:   ticket.Lamport(),
		ActorID:   types.ID(ticket.ActorID().String()),
		ServerSeq: serverSeq,
	}
	if raw == nil {
		syncedSeqInfo.ID = newID()
	} else {
		syncedSeqInfo.ID = raw.(*database.SyncedSeqInfo).ID
	}

	if err := txn.Insert(tblSyncedSeqs, syncedSeqInfo); err != nil {
		return fmt.Errorf("insert syncedseqs of %s: %w", docID.String(), err)
	}

	txn.Commit()
	return nil
}

// FindDocInfosByPaging returns the documentInfos of the given paging.
func (d *DB) FindDocInfosByPaging(
	ctx context.Context,
	projectID types.ID,
	paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var iterator memdb.ResultIterator
	var err error
	if paging.IsForward {
		iterator, err = txn.LowerBound(
			tblDocuments,
			"project_id_id",
			projectID.String(),
			paging.Offset.String(),
		)
	} else {
		offset := paging.Offset
		if paging.Offset == "" {
			offset = types.IDFromActorID(time.MaxActorID)
		}

		iterator, err = txn.ReverseLowerBound(
			tblDocuments,
			"project_id_id",
			projectID.String(),
			offset.String(),
		)
	}
	if err != nil {
		return nil, fmt.Errorf("fetch documents of %s: %w", projectID.String(), err)
	}

	var docInfos []*database.DocInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.DocInfo)
		if len(docInfos) >= paging.PageSize || info.ProjectID != projectID {
			break
		}

		if info.ID != paging.Offset && info.RemovedAt.IsZero() {
			docInfos = append(docInfos, info)
		}
	}

	return docInfos, nil
}

// FindDocInfosByQuery returns the docInfos which match the given query.
func (d *DB) FindDocInfosByQuery(
	ctx context.Context,
	projectID types.ID,
	query string,
	pageSize int,
) (*types.SearchResult[*database.DocInfo], error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.Get(tblDocuments, "project_id_key_prefix", projectID.String(), query)
	if err != nil {
		return nil, fmt.Errorf("find docInfos by query: %w", err)
	}

	var docInfos []*database.DocInfo
	count := 0
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		if count < pageSize {
			info := raw.(*database.DocInfo)
			docInfos = append(docInfos, info)
		}
		count++
	}

	return &types.SearchResult[*database.DocInfo]{
		TotalCount: count,
		Elements:   docInfos,
	}, nil
}

// IsDocumentAttached returns whether the document is attached to clients.
func (d *DB) IsDocumentAttached(
	ctx context.Context,
	projectID types.ID,
	docID types.ID,
	excludeClientID types.ID,
) (bool, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	it, err := txn.Get(tblClients, "project_id", projectID.String())
	if err != nil {
		return false, fmt.Errorf("%w", err)
	}
	if it == nil {
		return false, database.ErrClientNotFound
	}

	for raw := it.Next(); raw != nil; raw = it.Next() {
		clientInfo := raw.(*database.ClientInfo)
		if clientInfo.ID == excludeClientID {
			continue
		}
		clientDocInfo := clientInfo.Documents[docID]
		if clientDocInfo == nil {
			continue
		}
		if clientDocInfo.Status == database.DocumentAttached {
			return true, nil
		}
	}

	return false, nil
}

func (d *DB) findTicketByServerSeq(
	txn *memdb.Txn,
	docID types.ID,
	serverSeq int64,
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
		return nil, fmt.Errorf("fetch change of %s: %w", docID.String(), err)
	}
	if raw == nil {
		return nil, fmt.Errorf(
			"docID %s, serverSeq %d: %w",
			docID.String(),
			serverSeq,
			database.ErrDocumentNotFound,
		)
	}

	changeInfo := raw.(*database.ChangeInfo)
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
