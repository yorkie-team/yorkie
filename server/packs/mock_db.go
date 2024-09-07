/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

	"github.com/stretchr/testify/mock"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// MockDB represents a mock database for testing purposes
type MockDB struct {
	mock.Mock
	realDB database.Database
}

// NewMockDB returns a mock database with a real database
func NewMockDB(realDB database.Database) *MockDB {
	return &MockDB{
		realDB: realDB,
	}
}

// Close all resources of this client.
func (m *MockDB) Close() error {
	return m.realDB.Close()
}

// EnsureDefaultUserAndProject creates the default user and project if they do not exist.
func (m *MockDB) EnsureDefaultUserAndProject(
	ctx context.Context,
	username, password string,
	clientDeactivateThreshold string,
) (*database.UserInfo, *database.ProjectInfo, error) {
	return m.realDB.EnsureDefaultUserAndProject(ctx, username, password, clientDeactivateThreshold)
}

// CreateProjectInfo creates a new project.
func (m *MockDB) CreateProjectInfo(
	ctx context.Context,
	name string,
	owner types.ID,
	clientDeactivateThreshold string,
) (*database.ProjectInfo, error) {
	return m.realDB.CreateProjectInfo(ctx, name, owner, clientDeactivateThreshold)
}

// FindNextNCyclingProjectInfos finds the next N cycling projects from the given projectID.
func (m *MockDB) FindNextNCyclingProjectInfos(
	ctx context.Context,
	pageSize int,
	lastProjectID types.ID,
) ([]*database.ProjectInfo, error) {
	return m.realDB.FindNextNCyclingProjectInfos(ctx, pageSize, lastProjectID)
}

// ListProjectInfos returns all project infos owned by owner.
func (m *MockDB) ListProjectInfos(ctx context.Context, owner types.ID) ([]*database.ProjectInfo, error) {
	return m.realDB.ListProjectInfos(ctx, owner)
}

// FindProjectInfoByPublicKey returns a project by public key.
func (m *MockDB) FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*database.ProjectInfo, error) {
	return m.realDB.FindProjectInfoByPublicKey(ctx, publicKey)
}

// FindProjectInfoBySecretKey returns a project by secret key.
func (m *MockDB) FindProjectInfoBySecretKey(ctx context.Context, secretKey string) (*database.ProjectInfo, error) {
	return m.realDB.FindProjectInfoBySecretKey(ctx, secretKey)
}

// FindProjectInfoByName returns a project by name.
func (m *MockDB) FindProjectInfoByName(
	ctx context.Context,
	owner types.ID,
	name string,
) (*database.ProjectInfo, error) {
	return m.realDB.FindProjectInfoByName(ctx, owner, name)
}

// FindProjectInfoByID returns a project by the given id.
func (m *MockDB) FindProjectInfoByID(ctx context.Context, id types.ID) (*database.ProjectInfo, error) {
	return m.realDB.FindProjectInfoByID(ctx, id)
}

// UpdateProjectInfo updates the project info.
func (m *MockDB) UpdateProjectInfo(
	ctx context.Context,
	owner types.ID,
	id types.ID,
	fields *types.UpdatableProjectFields,
) (*database.ProjectInfo, error) {
	return m.realDB.UpdateProjectInfo(ctx, owner, id, fields)
}

// CreateUserInfo creates a new user.
func (m *MockDB) CreateUserInfo(
	ctx context.Context,
	username string,
	hashedPassword string,
) (*database.UserInfo, error) {
	return m.realDB.CreateUserInfo(ctx, username, hashedPassword)
}

// DeleteUserInfoByName deletes a user by name.
func (m *MockDB) DeleteUserInfoByName(ctx context.Context, username string) error {
	return m.realDB.DeleteUserInfoByName(ctx, username)
}

// ChangeUserPassword changes to new password for user.
func (m *MockDB) ChangeUserPassword(ctx context.Context, username, hashedNewPassword string) error {
	return m.realDB.ChangeUserPassword(ctx, username, hashedNewPassword)
}

// FindUserInfoByID returns a user by ID.
func (m *MockDB) FindUserInfoByID(ctx context.Context, clientID types.ID) (*database.UserInfo, error) {
	return m.realDB.FindUserInfoByID(ctx, clientID)
}

// FindUserInfoByName returns a user by username.
func (m *MockDB) FindUserInfoByName(ctx context.Context, username string) (*database.UserInfo, error) {
	return m.realDB.FindUserInfoByName(ctx, username)
}

// ListUserInfos returns all users.
func (m *MockDB) ListUserInfos(ctx context.Context) ([]*database.UserInfo, error) {
	return m.realDB.ListUserInfos(ctx)
}

// ActivateClient activates the client of the given key.
func (m *MockDB) ActivateClient(ctx context.Context, projectID types.ID, key string) (*database.ClientInfo, error) {
	return m.realDB.ActivateClient(ctx, projectID, key)
}

// DeactivateClient deactivates the client of the given refKey and updates document statuses as detached.
func (m *MockDB) DeactivateClient(ctx context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	return m.realDB.DeactivateClient(ctx, refKey)
}

// FindClientInfoByRefKey finds the client of the given refKey.
func (m *MockDB) FindClientInfoByRefKey(ctx context.Context, refKey types.ClientRefKey) (*database.ClientInfo, error) {
	return m.realDB.FindClientInfoByRefKey(ctx, refKey)
}

// UpdateClientInfoAfterPushPull updates the client from the given clientInfo after handling PushPull.
func (m *MockDB) UpdateClientInfoAfterPushPull(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docInfo *database.DocInfo,
) error {
	args := m.Called(ctx, clientInfo, docInfo)
	if args.Get(0) != nil {
		return fmt.Errorf("%w", args.Error(0))
	}
	return m.realDB.UpdateClientInfoAfterPushPull(ctx, clientInfo, docInfo)
}

// FindDeactivateCandidatesPerProject finds the clients that need housekeeping per project.
func (m *MockDB) FindDeactivateCandidatesPerProject(
	ctx context.Context,
	project *database.ProjectInfo,
	candidatesLimit int,
) ([]*database.ClientInfo, error) {
	return m.realDB.FindDeactivateCandidatesPerProject(ctx, project, candidatesLimit)
}

// FindDocInfoByKeyAndOwner finds the document of the given key.
func (m *MockDB) FindDocInfoByKeyAndOwner(
	ctx context.Context,
	clientRefKey types.ClientRefKey,
	docKey key.Key,
	createDocIfNotExist bool,
) (*database.DocInfo, error) {
	return m.realDB.FindDocInfoByKeyAndOwner(ctx, clientRefKey, docKey, createDocIfNotExist)
}

// FindDocInfoByKey finds the document of the given key.
func (m *MockDB) FindDocInfoByKey(ctx context.Context, projectID types.ID, docKey key.Key) (*database.DocInfo, error) {
	return m.realDB.FindDocInfoByKey(ctx, projectID, docKey)
}

// FindDocInfosByKeys finds the documents of the given keys.
func (m *MockDB) FindDocInfosByKeys(
	ctx context.Context,
	projectID types.ID,
	docKeys []key.Key,
) ([]*database.DocInfo, error) {
	return m.realDB.FindDocInfosByKeys(ctx, projectID, docKeys)
}

// FindDocInfoByRefKey finds a docInfo of the given refKey.
func (m *MockDB) FindDocInfoByRefKey(ctx context.Context, refKey types.DocRefKey) (*database.DocInfo, error) {
	return m.realDB.FindDocInfoByRefKey(ctx, refKey)
}

// UpdateDocInfoStatusToRemoved updates the document status to removed.
func (m *MockDB) UpdateDocInfoStatusToRemoved(ctx context.Context, refKey types.DocRefKey) error {
	return m.realDB.UpdateDocInfoStatusToRemoved(ctx, refKey)
}

// CreateChangeInfos stores the given changes and doc info.
func (m *MockDB) CreateChangeInfos(
	ctx context.Context,
	projectID types.ID,
	docInfo *database.DocInfo,
	initialServerSeq int64,
	changes []*change.Change,
	isRemoved bool,
) error {
	return m.realDB.CreateChangeInfos(ctx, projectID, docInfo, initialServerSeq, changes, isRemoved)
}

// PurgeStaleChanges deletes changes before the smallest in synced seqs.
func (m *MockDB) PurgeStaleChanges(ctx context.Context, docRefKey types.DocRefKey) error {
	return m.realDB.PurgeStaleChanges(ctx, docRefKey)
}

// FindChangesBetweenServerSeqs returns the changes between two server sequences.
func (m *MockDB) FindChangesBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*change.Change, error) {
	return m.realDB.FindChangesBetweenServerSeqs(ctx, docRefKey, from, to)
}

// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
func (m *MockDB) FindChangeInfosBetweenServerSeqs(
	ctx context.Context,
	docRefKey types.DocRefKey,
	from int64,
	to int64,
) ([]*database.ChangeInfo, error) {
	return m.realDB.FindChangeInfosBetweenServerSeqs(ctx, docRefKey, from, to)
}

// CreateSnapshotInfo stores the snapshot of the given document.
func (m *MockDB) CreateSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	doc *document.InternalDocument,
) error {
	return m.realDB.CreateSnapshotInfo(ctx, docRefKey, doc)
}

// FindSnapshotInfoByRefKey returns the snapshot by the given refKey.
func (m *MockDB) FindSnapshotInfoByRefKey(
	ctx context.Context,
	refKey types.SnapshotRefKey,
) (*database.SnapshotInfo, error) {
	return m.realDB.FindSnapshotInfoByRefKey(ctx, refKey)
}

// FindClosestSnapshotInfo finds the last snapshot of the given document.
func (m *MockDB) FindClosestSnapshotInfo(
	ctx context.Context,
	docRefKey types.DocRefKey,
	serverSeq int64,
	includeSnapshot bool,
) (*database.SnapshotInfo, error) {
	return m.realDB.FindClosestSnapshotInfo(ctx, docRefKey, serverSeq, includeSnapshot)
}

// FindMinSyncedSeqInfo finds the minimum synced sequence info.
func (m *MockDB) FindMinSyncedSeqInfo(ctx context.Context, docRefKey types.DocRefKey) (*database.SyncedSeqInfo, error) {
	return m.realDB.FindMinSyncedSeqInfo(ctx, docRefKey)
}

// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client and returns the min synced ticket.
func (m *MockDB) UpdateAndFindMinSyncedTicket(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	serverSeq int64,
) (*time.Ticket, error) {
	return m.realDB.UpdateAndFindMinSyncedTicket(ctx, clientInfo, docRefKey, serverSeq)
}

// FindDocInfosByPaging returns the docInfos of the given paging.
func (m *MockDB) FindDocInfosByPaging(
	ctx context.Context,
	projectID types.ID,
	paging types.Paging[types.ID],
) ([]*database.DocInfo, error) {
	return m.realDB.FindDocInfosByPaging(ctx, projectID, paging)
}

// FindDocInfosByQuery returns the docInfos which match the given query.
func (m *MockDB) FindDocInfosByQuery(
	ctx context.Context,
	projectID types.ID,
	query string,
	pageSize int,
) (*types.SearchResult[*database.DocInfo], error) {
	return m.realDB.FindDocInfosByQuery(ctx, projectID, query, pageSize)
}

// UpdateSyncedSeq updates the syncedSeq of the given client.
func (m *MockDB) UpdateSyncedSeq(
	ctx context.Context,
	clientInfo *database.ClientInfo,
	docRefKey types.DocRefKey,
	serverSeq int64,
) error {
	return m.realDB.UpdateSyncedSeq(ctx, clientInfo, docRefKey, serverSeq)
}

// IsDocumentAttached returns whether the given document is attached to clients.
func (m *MockDB) IsDocumentAttached(
	ctx context.Context,
	docRefKey types.DocRefKey,
	excludeClientID types.ID,
) (bool, error) {
	return m.realDB.IsDocumentAttached(ctx, docRefKey, excludeClientID)
}
