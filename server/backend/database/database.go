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

// Package database provides the database interface for the Yorkie backend.
package database

import (
	"context"
	"errors"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrProjectAlreadyExists is returned when the project already exists.
	ErrProjectAlreadyExists = errors.New("project already exists")

	// ErrUserNotFound is returned when the user is not found.
	ErrUserNotFound = errors.New("user not found")

	// ErrProjectNotFound is returned when the project is not found.
	ErrProjectNotFound = errors.New("project not found")

	// ErrUserAlreadyExists is returned when the user already exists.
	ErrUserAlreadyExists = errors.New("user already exists")

	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.New("client not found")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.New("document not found")

	// ErrSnapshotNotFound is returned when the snapshot could not be found.
	ErrSnapshotNotFound = errors.New("snapshot not found")

	// ErrConflictOnUpdate is returned when a conflict occurs during update.
	ErrConflictOnUpdate = errors.New("conflict on update")

	// ErrProjectNameAlreadyExists is returned when the project name already exists.
	ErrProjectNameAlreadyExists = errors.New("project name already exists")
)

// Database represents database which reads or saves Yorkie data.
type Database interface {
	// Close all resources of this database.
	Close() error

	// FindProjectInfoByPublicKey returns a project by public key.
	FindProjectInfoByPublicKey(
		ctx context.Context,
		publicKey string,
	) (*ProjectInfo, error)

	// FindProjectInfoByName returns a project by the given name.
	FindProjectInfoByName(
		ctx context.Context,
		owner string,
		name string,
	) (*ProjectInfo, error)

	// FindProjectInfoByID returns a project by the given id. It should not be
	// used directly by clients because it is not checked if the project is
	// permitted to be accessed by the admin client.
	FindProjectInfoByID(ctx context.Context, id types.ID) (*ProjectInfo, error)

	// EnsureDefaultUserAndProject ensures that the default user and project
	// exists.
	EnsureDefaultUserAndProject(
		ctx context.Context,
		username,
		password string,
		clientDeactivateThreshold string,
	) (*UserInfo, *ProjectInfo, error)

	// CreateProjectInfo creates a new project.
	CreateProjectInfo(
		ctx context.Context,
		name string,
		owner string,
		clientDeactivateThreshold string,
	) (*ProjectInfo, error)

	// ListProjectInfos returns all project infos owned by owner.
	ListProjectInfos(ctx context.Context, owner string) ([]*ProjectInfo, error)

	// UpdateProjectInfo updates the project.
	UpdateProjectInfo(
		ctx context.Context,
		owner string,
		id types.ID,
		fields *types.UpdatableProjectFields,
	) (*ProjectInfo, error)

	// CreateUserInfo creates a new user.
	CreateUserInfo(
		ctx context.Context,
		username string,
		hashedPassword string,
	) (*UserInfo, error)

	// FindUserInfo returns a user by the given username.
	FindUserInfo(ctx context.Context, username string) (*UserInfo, error)

	// ListUserInfos returns all users.
	ListUserInfos(ctx context.Context) ([]*UserInfo, error)

	// ActivateClient activates the client of the given key.
	ActivateClient(ctx context.Context, projectID types.ID, key string) (*ClientInfo, error)

	// DeactivateClient deactivates the client of the given key and ID.
	DeactivateClient(ctx context.Context, clientRef types.ClientRefKey) (*ClientInfo, error)

	// FindClientInfoByKeyAndID finds the client of the given key and ID.
	FindClientInfoByKeyAndID(ctx context.Context, clientRef types.ClientRefKey) (*ClientInfo, error)

	// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
	// after handling PushPull.
	UpdateClientInfoAfterPushPull(ctx context.Context, clientInfo *ClientInfo, docInfo *DocInfo) error

	// FindDeactivateCandidates finds the housekeeping candidates.
	FindDeactivateCandidates(
		ctx context.Context,
		candidatesLimitPerProject int,
		projectFetchSize int,
		lastProjectID types.ID,
	) (types.ID, []*ClientInfo, error)

	// FindDocInfoByKey finds the document of the given key.
	FindDocInfoByKey(
		ctx context.Context,
		projectID types.ID,
		docKey key.Key,
	) (*DocInfo, error)

	// FindDocInfoByKeyAndOwner finds the document of the given key. If the
	// createDocIfNotExist condition is true, create the document if it does not
	// exist.
	FindDocInfoByKeyAndOwner(
		ctx context.Context,
		projectID types.ID,
		docKey key.Key,
		ownerRef types.ClientRefKey,
		createDocIfNotExist bool,
	) (*DocInfo, error)

	// FindDocInfoByKeyAndID finds the document of the given key and ID.
	FindDocInfoByKeyAndID(
		ctx context.Context,
		docRef types.DocRefKey,
	) (*DocInfo, error)

	// UpdateDocInfoStatusToRemoved updates the document status to removed.
	UpdateDocInfoStatusToRemoved(
		ctx context.Context,
		docRef types.DocRefKey,
	) error

	// CreateChangeInfos stores the given changes then updates the given docInfo.
	CreateChangeInfos(
		ctx context.Context,
		docInfo *DocInfo,
		initialServerSeq int64,
		changes []*change.Change,
		isRemoved bool,
	) error

	// PurgeStaleChanges delete changes before the smallest in `syncedseqs` to
	// save storage.
	PurgeStaleChanges(
		ctx context.Context,
		docRef types.DocRefKey,
	) error

	// FindChangesBetweenServerSeqs returns the changes between two server sequences.
	FindChangesBetweenServerSeqs(
		ctx context.Context,
		docRef types.DocRefKey,
		from int64,
		to int64,
	) ([]*change.Change, error)

	// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docRef types.DocRefKey,
		from int64,
		to int64,
	) ([]*ChangeInfo, error)

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(
		ctx context.Context,
		docRef types.DocRefKey,
		doc *document.InternalDocument,
	) error

	// FindSnapshotInfo returns the snapshot by the given doc_key, doc_id and server_seq.
	FindSnapshotInfo(
		ctx context.Context,
		docRef types.DocRefKey,
		serverSeq int64,
	) (*SnapshotInfo, error)

	// FindClosestSnapshotInfo finds the closest snapshot info in a given serverSeq.
	FindClosestSnapshotInfo(
		ctx context.Context,
		docRef types.DocRefKey,
		serverSeq int64,
		includeSnapshot bool,
	) (*SnapshotInfo, error)

	// FindMinSyncedSeqInfo finds the minimum synced sequence info.
	FindMinSyncedSeqInfo(
		ctx context.Context,
		docRef types.DocRefKey,
	) (*SyncedSeqInfo, error)

	// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
	// and returns the min synced ticket.
	UpdateAndFindMinSyncedTicket(
		ctx context.Context,
		clientInfo *ClientInfo,
		docRef types.DocRefKey,
		serverSeq int64,
	) (*time.Ticket, error)

	// UpdateSyncedSeq updates the syncedSeq of the given client.
	UpdateSyncedSeq(
		ctx context.Context,
		clientInfo *ClientInfo,
		docRef types.DocRefKey,
		serverSeq int64,
	) error

	// FindDocInfosByPaging returns the documentInfos of the given paging.
	FindDocInfosByPaging(
		ctx context.Context,
		projectID types.ID,
		paging types.Paging[types.DocRefKey],
	) ([]*DocInfo, error)

	// FindDocInfosByQuery returns the documentInfos which match the given query.
	FindDocInfosByQuery(
		ctx context.Context,
		projectID types.ID,
		query string,
		pageSize int,
	) (*types.SearchResult[*DocInfo], error)

	// IsDocumentAttached returns true if the document is attached to clients.
	IsDocumentAttached(
		ctx context.Context,
		projectID types.ID,
		docRef types.DocRefKey,
		excludeClientRef types.ClientRefKey,
	) (bool, error)
}
