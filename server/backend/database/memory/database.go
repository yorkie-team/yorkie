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
	_ context.Context,
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

// FindProjectInfoBySecretKey returns a project by secret key.
func (d *DB) FindProjectInfoBySecretKey(
	_ context.Context,
	secretKey string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblProjects, "secret_key", secretKey)
	if err != nil {
		return nil, fmt.Errorf("find project by secret key: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", secretKey, database.ErrProjectNotFound)
	}

	return raw.(*database.ProjectInfo).DeepCopy(), nil
}

// FindProjectInfoByName returns a project by the given name.
func (d *DB) FindProjectInfoByName(
	_ context.Context,
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
func (d *DB) FindProjectInfoByID(_ context.Context, id types.ID) (*database.ProjectInfo, error) {
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
	_ context.Context,
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
	_ context.Context,
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
	_ context.Context,
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

// FindNextNCyclingProjectInfos finds the next N cycling projects from the given projectID.
func (d *DB) FindNextNCyclingProjectInfos(
	_ context.Context,
	pageSize int,
	lastProjectID types.ID,
) ([]*database.ProjectInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.LowerBound(
		tblProjects,
		"id",
		lastProjectID.String(),
	)
	if err != nil {
		return nil, fmt.Errorf("fetch projects: %w", err)
	}

	var infos []*database.ProjectInfo
	isCircular := false

	for i := 0; i < pageSize; i++ {
		raw := iter.Next()
		if raw == nil {
			if isCircular {
				break
			}

			iter, err = txn.LowerBound(
				tblProjects,
				"id",
				database.DefaultProjectID.String(),
			)
			if err != nil {
				return nil, fmt.Errorf("fetch projects: %w", err)
			}

			i--
			isCircular = true
			continue
		}
		info := raw.(*database.ProjectInfo).DeepCopy()

		if i == 0 && info.ID == lastProjectID {
			pageSize++
			continue
		}

		if len(infos) > 0 && infos[0].ID == info.ID {
			break
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// ListProjectInfos returns all project infos owned by owner.
func (d *DB) ListProjectInfos(
	_ context.Context,
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
	_ context.Context,
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
	_ context.Context,
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

// GetOrCreateUserInfoByGitHubID returns a user by the given GitHub ID.
func (d *DB) GetOrCreateUserInfoByGitHubID(
	_ context.Context,
	githubID string,
) (*database.UserInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", githubID)
	if err != nil {
		return nil, fmt.Errorf("find user by github id: %w", err)
	}

	now := gotime.Now()
	var info *database.UserInfo

	if raw == nil {
		info = &database.UserInfo{
			ID:           newID(),
			Username:     githubID,
			AuthProvider: "github",
			CreatedAt:    now,
			AccessedAt:   now,
		}
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("create user: %w", err)
		}
	} else {
		info = raw.(*database.UserInfo).DeepCopy()
		info.AccessedAt = now
		if err := txn.Insert(tblUsers, info); err != nil {
			return nil, fmt.Errorf("update user: %w", err)
		}
	}

	txn.Commit()
	return info, nil
}

// DeleteUserInfoByName deletes a user by name.
func (d *DB) DeleteUserInfoByName(_ context.Context, username string) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return fmt.Errorf("find user by username: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
	}

	info := raw.(*database.UserInfo).DeepCopy()
	if err = txn.Delete(tblUsers, info); err != nil {
		return fmt.Errorf("delete account %s: %w", info.ID, err)
	}

	txn.Commit()
	return nil
}

// ChangeUserPassword changes to new password.
func (d *DB) ChangeUserPassword(_ context.Context, username, hashedNewPassword string) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "username", username)
	if err != nil {
		return fmt.Errorf("find user by username: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", username, database.ErrUserNotFound)
	}

	info := raw.(*database.UserInfo).DeepCopy()
	info.HashedPassword = hashedNewPassword
	if err := txn.Insert(tblUsers, info); err != nil {
		return fmt.Errorf("change password user: %w", err)
	}

	txn.Commit()

	return nil
}

// FindUserInfoByID finds a user by the given ID.
func (d *DB) FindUserInfoByID(_ context.Context, clientID types.ID) (*database.UserInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblUsers, "id", clientID.String())
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", clientID, database.ErrUserNotFound)
	}

	return raw.(*database.UserInfo).DeepCopy(), nil
}

// FindUserInfoByName finds a user by the given username.
func (d *DB) FindUserInfoByName(_ context.Context, username string) (*database.UserInfo, error) {
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
func (d *DB) ListUserInfos(_ context.Context) ([]*database.UserInfo, error) {
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
	_ context.Context,
	projectID types.ID,
	key string,
	metadata map[string]string,
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
		Metadata:  metadata,
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
func (d *DB) DeactivateClient(_ context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	if err := refKey.ClientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", refKey.ClientID.String())
	if err != nil {
		return nil, fmt.Errorf("find client by id: %w", err)
	}

	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(refKey.ProjectID); err != nil {
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

// FindClientInfoByRefKey finds a client by the given refKey.
func (d *DB) FindClientInfoByRefKey(_ context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	if err := refKey.ClientID.Validate(); err != nil {
		return nil, err
	}

	txn := d.db.Txn(false)
	defer txn.Abort()

	raw, err := txn.First(tblClients, "id", refKey.ClientID.String())
	if err != nil {
		return nil, fmt.Errorf("find client by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey.ClientID, database.ErrClientNotFound)
	}

	clientInfo := raw.(*database.ClientInfo)
	if err := clientInfo.CheckIfInProject(refKey.ProjectID); err != nil {
		return nil, err
	}

	return clientInfo.DeepCopy(), nil
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
// after handling PushPull.
func (d *DB) UpdateClientInfoAfterPushPull(
	_ context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	docRefKey := docInfo.RefKey()
	clientDocInfo := clientInfo.Documents[docRefKey.DocID]
	attached, err := clientInfo.IsAttached(docRefKey.DocID)
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
		loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{
			Status: clientDocInfo.Status,
		}
		loaded.UpdatedAt = gotime.Now()
	} else {
		if _, ok := loaded.Documents[docRefKey.DocID]; !ok {
			loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{}
		}

		loadedClientDocInfo := loaded.Documents[docRefKey.DocID]
		serverSeq := max(clientDocInfo.ServerSeq, loadedClientDocInfo.ServerSeq)
		clientSeq := max(clientDocInfo.ClientSeq, loadedClientDocInfo.ClientSeq)
		loaded.Documents[docRefKey.DocID] = &database.ClientDocInfo{
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

// FindDeactivateCandidatesPerProject finds the clients that need housekeeping per project.
func (d *DB) FindDeactivateCandidatesPerProject(
	_ context.Context,
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

		if info.ProjectID == project.ID {
			infos = append(infos, info)
		}
	}
	return infos, nil
}

// FindCompactionCandidatesPerProject finds the documents that need compaction per project.
func (d *DB) FindCompactionCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
	compactionMinChanges int,
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*database.DocInfo
	iterator, err := txn.Get(tblDocuments, "project_id", project.ID.String())
	if err != nil {
		return nil, fmt.Errorf("fetch documents: %w", err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.DocInfo)
		if candidatesLimit <= len(infos) {
			break
		}

		// 1. Check if the document is attached to a client.
		isAttached, err := d.IsDocumentAttached(ctx, types.DocRefKey{
			ProjectID: project.ID,
			DocID:     info.ID,
		}, "")
		if err != nil {
			return nil, err
		}
		if isAttached {
			continue
		}

		// 2. Check if the document has enough changes to compact.
		if info.ServerSeq < int64(compactionMinChanges) {
			continue
		}

		infos = append(infos, info)
	}
	return infos, nil
}

// FindAttachedClientInfosByRefKey finds the client infos of the given document.
func (d *DB) FindAttachedClientInfosByRefKey(
	_ context.Context,
	docRefKey types.DocRefKey,
) ([]*database.ClientInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", docRefKey.ProjectID.String())
	if err != nil {
		return nil, fmt.Errorf("find client infos by attached doc ref key: %w", err)
	}

	var infos []*database.ClientInfo
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ClientInfo)

		if info.Documents[docRefKey.DocID] != nil && info.Documents[docRefKey.DocID].Status == database.DocumentAttached {
			infos = append(infos, info)
		}
	}
	return infos, nil
}

// FindOrCreateDocInfo finds the document or creates it if it does not exist.
func (d *DB) FindOrCreateDocInfo(
	_ context.Context,
	clientRefKey types.ClientRefKey,
	key key.Key,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	info, err := d.findDocInfoByKey(txn, clientRefKey.ProjectID, key)
	if err != nil {
		return info, err
	}

	if info == nil {
		now := gotime.Now()
		info = &database.DocInfo{
			ID:         newID(),
			ProjectID:  clientRefKey.ProjectID,
			Key:        key,
			Owner:      clientRefKey.ClientID,
			ServerSeq:  0,
			CreatedAt:  now,
			UpdatedAt:  now,
			AccessedAt: now,
		}
		if err := txn.Insert(tblDocuments, info); err != nil {
			return nil, fmt.Errorf("create document: %w", err)
		}
		txn.Commit()
	}

	return info.DeepCopy(), nil
}

// findDocInfoByKey finds the document of the given key.
func (d *DB) findDocInfoByKey(txn *memdb.Txn, projectID types.ID, key key.Key) (*database.DocInfo, error) {
	// TODO(hackerwins): Removed documents should be filtered out by the query, but
	// somehow it does not work. This is a workaround.
	// val, err := txn.First(tblDocuments, "project_id_key_removed_at", projectID.String(), key.String(), gotime.Time{})
	iter, err := txn.Get(
		tblDocuments,
		"project_id_key_removed_at",
		projectID.String(),
		key.String(),
		gotime.Time{},
	)
	if err != nil {
		return nil, fmt.Errorf("find doc info by key: %w", err)
	}
	var docInfo *database.DocInfo
	for val := iter.Next(); val != nil; val = iter.Next() {
		if info := val.(*database.DocInfo); info.RemovedAt.IsZero() {
			docInfo = info
		}
	}

	return docInfo, nil
}

// FindDocInfoByKey finds the document of the given key.
func (d *DB) FindDocInfoByKey(
	_ context.Context,
	projectID types.ID,
	key key.Key,
) (*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	info, err := d.findDocInfoByKey(txn, projectID, key)
	if err != nil {
		return nil, fmt.Errorf("find doc info by key: %w", err)
	}
	if info == nil {
		return nil, fmt.Errorf("%s: %w", key, database.ErrDocumentNotFound)
	}

	return info.DeepCopy(), nil
}

// FindDocInfosByKeys finds the documents of the given keys.
func (d *DB) FindDocInfosByKeys(
	_ context.Context,
	projectID types.ID,
	keys []key.Key,
) ([]*database.DocInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	var infos []*database.DocInfo
	for _, k := range keys {
		info, err := d.findDocInfoByKey(txn, projectID, k)
		if err != nil {
			return nil, fmt.Errorf("find doc info by key: %w", err)
		}
		if info == nil {
			continue
		}

		infos = append(infos, info.DeepCopy())
	}

	return infos, nil
}

// FindDocInfoByRefKey finds a docInfo of the given refKey.
func (d *DB) FindDocInfoByRefKey(
	_ context.Context,
	refKey types.DocRefKey,
) (*database.DocInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("find document by id: %w", err)
	}

	if raw == nil {
		return nil, fmt.Errorf("finding doc info by ID(%s): %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != refKey.ProjectID {
		return nil, fmt.Errorf("finding doc info by ID(%s): %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	return docInfo.DeepCopy(), nil
}

// UpdateDocInfoStatusToRemoved updates the status of the document to removed.
func (d *DB) UpdateDocInfoStatusToRemoved(
	_ context.Context,
	refKey types.DocRefKey,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return fmt.Errorf("find document by id: %w", err)
	}

	if raw == nil {
		return fmt.Errorf("finding doc info by ID(%s): %w", refKey.DocID, database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != refKey.ProjectID {
		return fmt.Errorf("finding doc info by ID(%s): %w", refKey.DocID, database.ErrDocumentNotFound)
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

// GetDocumentsCount returns the number of documents in the given project.
func (d *DB) GetDocumentsCount(
	_ context.Context,
	projectID types.ID,
) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblDocuments, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("fetch documents: %w", err)
	}

	count := int64(0)
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.DocInfo).DeepCopy()
		if !info.RemovedAt.IsZero() {
			continue
		}
		count++
	}

	return count, nil
}

// GetClientsCount returns the number of active clients in the given project.
func (d *DB) GetClientsCount(ctx context.Context, projectID types.ID) (int64, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iter, err := txn.Get(tblClients, "project_id", projectID.String())
	if err != nil {
		return 0, fmt.Errorf("fetch clients: %w", err)
	}

	count := int64(0)
	for raw := iter.Next(); raw != nil; raw = iter.Next() {
		info := raw.(*database.ClientInfo).DeepCopy()
		if info.Status != database.ClientActivated {
			continue
		}
		count++
	}

	return count, nil
}

// CreateChangeInfos stores the given changes and doc info. If the
// removeDoc condition is true, mark IsRemoved to true in doc info.
func (d *DB) CreateChangeInfos(
	ctx context.Context,
	refKey types.DocRefKey,
	checkpoint change.Checkpoint,
	changes []*database.ChangeInfo,
	isRemoved bool,
) (*database.DocInfo, change.Checkpoint, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", refKey.DocID.String())
	if err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("find document by id: %w", err)
	}
	if raw == nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("find document by id: %w", database.ErrDocumentNotFound)
	}

	docInfo := raw.(*database.DocInfo).DeepCopy()
	if docInfo.ProjectID != refKey.ProjectID {
		return nil, change.InitialCheckpoint, fmt.Errorf("find document by id: %w", database.ErrDocumentNotFound)
	}
	initialServerSeq := docInfo.ServerSeq

	for _, cn := range changes {
		serverSeq := docInfo.IncreaseServerSeq()
		checkpoint = checkpoint.NextServerSeq(serverSeq)
		cn.ServerSeq = serverSeq
		checkpoint = checkpoint.SyncClientSeq(cn.ClientSeq)

		if err := txn.Insert(tblChanges, &database.ChangeInfo{
			ID:             newID(),
			ProjectID:      docInfo.ProjectID,
			DocID:          docInfo.ID,
			ServerSeq:      cn.ServerSeq,
			ClientSeq:      cn.ClientSeq,
			Lamport:        cn.Lamport,
			ActorID:        cn.ActorID,
			VersionVector:  cn.VersionVector,
			Message:        cn.Message,
			Operations:     cn.Operations,
			PresenceChange: cn.PresenceChange,
		}); err != nil {
			return nil, change.InitialCheckpoint, fmt.Errorf("create change: %w", err)
		}
	}

	raw, err = txn.First(
		tblDocuments,
		"project_id_id",
		docInfo.ProjectID.String(),
		docInfo.ID.String(),
	)
	if err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("find document: %w", err)
	}
	if raw == nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("%s: %w", docInfo.ID, database.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*database.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != initialServerSeq {
		return nil, change.InitialCheckpoint, fmt.Errorf("%s: %w", docInfo.ID, database.ErrConflictOnUpdate)
	}

	now := gotime.Now()
	loadedDocInfo.ServerSeq = docInfo.ServerSeq

	for _, cn := range changes {
		if len(cn.Operations) > 0 {
			loadedDocInfo.UpdatedAt = now
			break
		}
	}

	if isRemoved {
		loadedDocInfo.RemovedAt = now
	}
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return nil, change.InitialCheckpoint, fmt.Errorf("update document: %w", err)
	}
	txn.Commit()

	if isRemoved {
		docInfo.RemovedAt = now
	}

	return docInfo, checkpoint, nil
}

// CompactChangeInfos stores the given compacted changes then updates the docInfo.
func (d *DB) CompactChangeInfos(
	ctx context.Context,
	docInfo *database.DocInfo,
	lastServerSeq int64,
	changes []*change.Change,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// 1. Purge the resources of the document.
	if _, err := d.purgeDocumentInternals(ctx, docInfo.ProjectID, docInfo.ID, txn); err != nil {
		return err
	}

	// 2. Store compacted change and update document
	raw, err := txn.First(
		tblDocuments,
		"project_id_id",
		docInfo.ProjectID.String(),
		docInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("find document: %w", err)
	}
	if raw == nil {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrDocumentNotFound)
	}
	loadedDocInfo := raw.(*database.DocInfo).DeepCopy()
	if loadedDocInfo.ServerSeq != lastServerSeq {
		return fmt.Errorf("%s: %w", docInfo.ID, database.ErrConflictOnUpdate)
	}

	if len(changes) == 0 {
		loadedDocInfo.ServerSeq = 0
	} else if len(changes) == 1 {
		loadedDocInfo.ServerSeq = 1
	} else {
		return fmt.Errorf("invalid number of changes: %d", len(changes))
	}

	for _, cn := range changes {
		encodedOperations, err := database.EncodeOperations(cn.Operations())
		if err != nil {
			return err
		}

		if err := txn.Insert(tblChanges, &database.ChangeInfo{
			ID:             newID(),
			ProjectID:      docInfo.ProjectID,
			DocID:          docInfo.ID,
			ServerSeq:      loadedDocInfo.ServerSeq,
			ClientSeq:      cn.ClientSeq(),
			Lamport:        cn.ID().Lamport(),
			ActorID:        types.ID(cn.ID().ActorID().String()),
			VersionVector:  cn.ID().VersionVector(),
			Message:        cn.Message(),
			Operations:     encodedOperations,
			PresenceChange: cn.PresenceChange(),
		}); err != nil {
			return fmt.Errorf("store change: %w", err)
		}
	}

	// 3. Update document
	now := gotime.Now()
	loadedDocInfo.CompactedAt = now
	if err := txn.Insert(tblDocuments, loadedDocInfo); err != nil {
		return fmt.Errorf("update document: %w", err)
	}

	txn.Commit()
	return nil
}

// FindLatestChangeInfoByActor returns the latest change created by given actorID.
func (d *DB) FindLatestChangeInfoByActor(
	_ context.Context,
	docRefKey types.DocRefKey,
	actorID types.ID,
	serverSeq int64,
) (*database.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblChanges,
		"doc_id_actor_id_server_seq",
		docRefKey.DocID.String(),
		actorID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch changes of %s: %w", actorID, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if info != nil && info.ActorID == actorID {
			return info, nil
		}
	}

	return nil, database.ErrChangeNotFound
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (d *DB) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*change.Change, error) {
	infos, err := d.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
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
	_ context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	if from > to {
		return nil, nil
	}
	var infos []*database.ChangeInfo

	iterator, err := txn.LowerBound(
		tblChanges,
		"doc_id_server_seq",
		docRefKey.DocID.String(),
		from,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch changes from %d: %w", from, err)
	}

	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.ChangeInfo)
		if info.DocID != docRefKey.DocID || info.ServerSeq > to {
			break
		}
		infos = append(infos, info.DeepCopy())
	}
	return infos, nil
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (d *DB) CreateSnapshotInfo(
	_ context.Context,
	docRefKey types.DocRefKey,
	doc *document.InternalDocument,
) error {
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), doc.AllPresences())
	if err != nil {
		return err
	}

	txn := d.db.Txn(true)
	defer txn.Abort()

	if err := txn.Insert(tblSnapshots, &database.SnapshotInfo{
		ID:            newID(),
		ProjectID:     docRefKey.ProjectID,
		DocID:         docRefKey.DocID,
		ServerSeq:     doc.Checkpoint().ServerSeq,
		Lamport:       doc.Lamport(),
		VersionVector: doc.VersionVector().DeepCopy(),
		Snapshot:      snapshot,
		CreatedAt:     gotime.Now(),
	}); err != nil {
		return fmt.Errorf("create snapshot: %w", err)
	}
	txn.Commit()
	return nil
}

// FindSnapshotInfoByRefKey returns the snapshot by the given refKey.
func (d *DB) FindSnapshotInfoByRefKey(
	_ context.Context,
	refKey types.SnapshotRefKey,
) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	raw, err := txn.First(tblSnapshots, "doc_id_server_seq",
		refKey.DocID.String(),
		refKey.ServerSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("find snapshot by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", refKey, database.ErrSnapshotNotFound)
	}

	return raw.(*database.SnapshotInfo).DeepCopy(), nil
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (d *DB) FindClosestSnapshotInfo(
	_ context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	iterator, err := txn.ReverseLowerBound(
		tblSnapshots,
		"doc_id_server_seq",
		docRefKey.DocID.String(),
		serverSeq,
	)
	if err != nil {
		return nil, fmt.Errorf("fetch snapshots before %d: %w", serverSeq, err)
	}

	var snapshotInfo *database.SnapshotInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		info := raw.(*database.SnapshotInfo)
		if info.DocID == docRefKey.DocID {
			snapshotInfo = &database.SnapshotInfo{
				ID:            info.ID,
				ProjectID:     info.ProjectID,
				DocID:         info.DocID,
				ServerSeq:     info.ServerSeq,
				Lamport:       info.Lamport,
				VersionVector: info.VersionVector,
				CreatedAt:     info.CreatedAt,
			}
			if includeSnapshot {
				snapshotInfo.Snapshot = info.Snapshot
			}
			break
		}
	}

	if snapshotInfo == nil {
		return &database.SnapshotInfo{
			VersionVector: time.NewVersionVector(),
		}, nil
	}

	return snapshotInfo, nil
}

// updateVersionVector updates the given serverSeq of the given client
func (d *DB) updateVersionVector(
	_ context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	versionVector time.VersionVector,
) error {
	txn := d.db.Txn(true)
	defer txn.Abort()

	isAttached, err := clientInfo.IsAttached(docRefKey.DocID)
	if err != nil {
		return err
	}

	if !isAttached {
		if _, err = txn.DeleteAll(
			tblVersionVectors,
			"doc_id_client_id",
			docRefKey.DocID.String(),
			clientInfo.ID.String(),
		); err != nil {
			return fmt.Errorf("delete version vector of %s: %w", docRefKey.DocID, err)
		}

		txn.Commit()
		return nil
	}

	raw, err := txn.First(
		tblVersionVectors,
		"doc_id_client_id",
		docRefKey.DocID.String(),
		clientInfo.ID.String(),
	)
	if err != nil {
		return fmt.Errorf("fetch version vector of %s: %w", docRefKey.DocID, err)
	}

	versionVectorInfo := &database.VersionVectorInfo{
		DocID:         docRefKey.DocID,
		ClientID:      clientInfo.ID,
		VersionVector: versionVector,
	}
	if raw == nil {
		versionVectorInfo.ID = newID()
	} else {
		versionVectorInfo.ID = raw.(*database.VersionVectorInfo).ID
	}

	if err := txn.Insert(tblVersionVectors, versionVectorInfo); err != nil {
		return fmt.Errorf("insert version vector of %s: %w", docRefKey.DocID, err)
	}

	txn.Commit()

	return nil
}

// UpdateMinVersionVector updates the version vector of the given client
// and returns the minimum version vector of all clients.
func (d *DB) UpdateMinVersionVector(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	// 01. Update synced version vector of the given client and document.
	// TODO(JOOHOJANG): We have to consider removing detached client's lamport
	// from min version vector.
	if err := d.updateVersionVector(ctx, clientInfo, docRefKey, vector); err != nil {
		return nil, err
	}

	// 02. Compute min version vector.
	return d.GetMinVersionVector(ctx, docRefKey, vector)
}

// GetMinVersionVector returns the minimum version vector of the given document.
func (d *DB) GetMinVersionVector(
	_ context.Context,
	docRefKey types.DocRefKey,
	vector time.VersionVector,
) (time.VersionVector, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()
	iterator, err := txn.Get(tblVersionVectors, "doc_id", docRefKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("find all version vectors: %w", err)
	}

	var infos []database.VersionVectorInfo
	for raw := iterator.Next(); raw != nil; raw = iterator.Next() {
		vvi := raw.(*database.VersionVectorInfo)
		infos = append(infos, *vvi)
	}

	var vectors []time.VersionVector
	vectors = append(vectors, vector)
	for _, vv := range infos {
		vectors = append(vectors, vv.VersionVector)
	}

	return time.MinVersionVector(vectors...), nil
}

// FindDocInfosByPaging returns the documentInfos of the given paging.
func (d *DB) FindDocInfosByPaging(
	_ context.Context,
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
		return nil, fmt.Errorf("fetch documents of %s: %w", projectID, err)
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
	_ context.Context,
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
			if info.IsRemoved() {
				continue
			}
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
	_ context.Context,
	refKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	txn := d.db.Txn(false)
	defer txn.Abort()

	it, err := txn.Get(tblClients, "project_id", refKey.ProjectID.String())
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
		clientDocInfo := clientInfo.Documents[refKey.DocID]
		if clientDocInfo == nil {
			continue
		}
		if clientDocInfo.Status == database.DocumentAttached {
			return true, nil
		}
	}

	return false, nil
}

// PurgeDocument purges the given document and its metadata from the database.
func (d *DB) PurgeDocument(
	ctx context.Context,
	docRefKey types.DocRefKey,
) (map[string]int64, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	raw, err := txn.First(tblDocuments, "id", docRefKey.DocID.String())
	if err != nil {
		return nil, fmt.Errorf("find document by id: %w", err)
	}

	docInfo := raw.(*database.DocInfo)
	if docInfo.ProjectID != docRefKey.ProjectID {
		return nil, fmt.Errorf("finding doc info by ID(%s): %w", docRefKey.DocID, database.ErrDocumentNotFound)
	}

	res, err := d.purgeDocumentInternals(ctx, docRefKey.ProjectID, docRefKey.DocID, txn)
	if err != nil {
		return nil, err
	}

	if err := txn.Delete(tblDocuments, docInfo); err != nil {
		return nil, fmt.Errorf("delete document: %w", err)
	}

	txn.Commit()
	return res, nil
}

func (d *DB) purgeDocumentInternals(
	_ context.Context,
	_ types.ID,
	docID types.ID,
	txn *memdb.Txn,
) (map[string]int64, error) {
	counts := make(map[string]int64)

	count, err := txn.DeleteAll(tblChanges, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge changes: %w", err)
	}
	counts[tblChanges] = int64(count)

	count, err = txn.DeleteAll(tblSnapshots, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge snapshots: %w", err)
	}
	counts[tblSnapshots] = int64(count)

	count, err = txn.DeleteAll(tblVersionVectors, "doc_id", docID.String())
	if err != nil {
		return nil, fmt.Errorf("purge version vectors: %w", err)
	}
	counts[tblVersionVectors] = int64(count)

	return counts, nil
}

func newID() types.ID {
	return types.ID(primitive.NewObjectID().Hex())
}

// RotateProjectKeys rotates the API keys of the project.
func (d *DB) RotateProjectKeys(
	_ context.Context,
	owner types.ID,
	id types.ID,
	publicKey string,
	secretKey string,
) (*database.ProjectInfo, error) {
	txn := d.db.Txn(true)
	defer txn.Abort()

	// Find project by ID and owner
	raw, err := txn.First(tblProjects, "id", id.String())
	if err != nil {
		return nil, fmt.Errorf("find project by id: %w", err)
	}
	if raw == nil {
		return nil, fmt.Errorf("%s: %w", id, database.ErrProjectNotFound)
	}

	project := raw.(*database.ProjectInfo).DeepCopy()
	if project.Owner != owner {
		return nil, database.ErrProjectNotFound
	}

	// Update project keys
	project.PublicKey = publicKey
	project.SecretKey = secretKey
	project.UpdatedAt = gotime.Now()

	// Save updated project
	if err := txn.Insert(tblProjects, project); err != nil {
		return nil, fmt.Errorf("update project: %w", err)
	}

	txn.Commit()
	return project, nil
}
