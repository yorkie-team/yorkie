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
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
)

var (
	// ErrProjectAlreadyExists is returned when the project already exists.
	ErrProjectAlreadyExists = errors.AlreadyExists("project already exists").WithCode("ErrProjectAlreadyExists")

	// ErrUserNotFound is returned when the user is not found.
	ErrUserNotFound = errors.NotFound("user not found").WithCode("ErrUserNotFound")

	// ErrProjectNotFound is returned when the project is not found.
	ErrProjectNotFound = errors.NotFound("project not found").WithCode("ErrProjectNotFound")

	// ErrUserAlreadyExists is returned when the user already exists.
	ErrUserAlreadyExists = errors.AlreadyExists("user already exists").WithCode("ErrUserAlreadyExists")

	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.NotFound("client not found").WithCode("ErrClientNotFound")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.NotFound("document not found").WithCode("ErrDocumentNotFound")

	// ErrChangeNotFound is returned when the change could not be found.
	ErrChangeNotFound = errors.NotFound("change not found").WithCode("ErrChangeNotFound")

	// ErrSnapshotNotFound is returned when the snapshot could not be found.
	ErrSnapshotNotFound = errors.NotFound("snapshot not found").WithCode("ErrSnapshotNotFound")

	// ErrConflictOnUpdate is returned when a conflict occurs during update.
	ErrConflictOnUpdate = errors.FailedPrecond("conflict on update").WithCode("ErrConflictOnUpdate")

	// ErrVersionVectorNotFound is returned when the version vector could not be found.
	ErrVersionVectorNotFound = errors.NotFound("version vector not found").WithCode("ErrVersionVectorNotFound")

	// ErrSchemaNotFound is returned when the schema could not be found.
	ErrSchemaNotFound = errors.NotFound("schema not found").WithCode("ErrSchemaNotFound")

	// ErrSchemaAlreadyExists is returned when the schema already exists.
	ErrSchemaAlreadyExists = errors.AlreadyExists("schema already exists").WithCode("ErrSchemaAlreadyExists")

	// ErrInvalidLeaseToken is returned when the provided token is invalid.
	ErrInvalidLeaseToken = errors.InvalidArgument("invalid lease token").WithCode("ErrInvalidLeaseToken")
)

// Database represents database which reads or saves Yorkie data.
type Database interface {
	// Close all resources of this database.
	Close() error

	// TryLeadership attempts to acquire or renew leadership with the given lease duration.
	// If leaseToken is empty, it attempts to acquire new leadership.
	// If leaseToken is provided, it attempts to renew the existing lease.
	TryLeadership(
		ctx context.Context,
		rpcAddr string,
		leaseToken string,
		leaseDuration gotime.Duration,
	) (*ClusterNodeInfo, error)

	// RemoveClusterNode removes the cluster node identified by rpcAddr.
	RemoveClusterNode(ctx context.Context, rpcAddr string) error

	// RemoveClusterNodes removes the current cluster node information for testing purposes.
	RemoveClusterNodes(ctx context.Context) error

	// FindClusterNodes returns nodes considered active within the given time window.
	FindClusterNodes(
		ctx context.Context,
		window gotime.Duration,
	) ([]*ClusterNodeInfo, error)

	// FindProjectInfoByPublicKey returns a project by public key.
	FindProjectInfoByPublicKey(
		ctx context.Context,
		publicKey string,
	) (*ProjectInfo, error)

	// FindProjectInfoBySecretKey returns a project by secret key.
	FindProjectInfoBySecretKey(
		ctx context.Context,
		secretKey string,
	) (*ProjectInfo, error)

	// FindProjectInfoByName returns a project by the given name.
	FindProjectInfoByName(
		ctx context.Context,
		owner types.ID,
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
	) (*UserInfo, *ProjectInfo, error)

	// CreateProjectInfo creates a new project.
	CreateProjectInfo(
		ctx context.Context,
		name string,
		owner types.ID,
	) (*ProjectInfo, error)

	// ListProjectInfos returns all project infos owned by owner.
	ListProjectInfos(ctx context.Context, owner types.ID) ([]*ProjectInfo, error)

	// UpdateProjectInfo updates the project.
	UpdateProjectInfo(
		ctx context.Context,
		owner types.ID,
		id types.ID,
		fields *types.UpdatableProjectFields,
	) (*ProjectInfo, error)

	// RotateProjectKeys rotates the API keys of the project.
	RotateProjectKeys(
		ctx context.Context,
		owner types.ID,
		id types.ID,
		publicKey string,
		secretKey string,
	) (*ProjectInfo, error)

	// CreateUserInfo creates a new user.
	CreateUserInfo(
		ctx context.Context,
		username string,
		hashedPassword string,
	) (*UserInfo, error)

	// GetOrCreateUserInfoByGitHubID returns a user by the given GitHub ID.
	GetOrCreateUserInfoByGitHubID(ctx context.Context, githubID string) (*UserInfo, error)

	// DeleteUserInfoByName deletes a user by name.
	DeleteUserInfoByName(ctx context.Context, username string) error

	// ChangeUserPassword changes to new password for user.
	ChangeUserPassword(ctx context.Context, username, hashedNewPassword string) error

	// FindUserInfoByID returns a user by the given ID.
	FindUserInfoByID(ctx context.Context, id types.ID) (*UserInfo, error)

	// FindUserInfoByName returns a user by the given username.
	FindUserInfoByName(ctx context.Context, username string) (*UserInfo, error)

	// ListUserInfos returns all users.
	ListUserInfos(ctx context.Context) ([]*UserInfo, error)

	// ActivateClient activates the client of the given key.
	ActivateClient(ctx context.Context, projectID types.ID, key string, metadata map[string]string) (*ClientInfo, error)

	// DeactivateClient deactivates the client of the given refKey.
	DeactivateClient(ctx context.Context, refKey types.ClientRefKey) (*ClientInfo, error)

	// TryAttaching updates the status of the document to Attaching to prevent
	// deactivating the client while the document is being attached.
	TryAttaching(ctx context.Context, refKey types.ClientRefKey, docID types.ID) (*ClientInfo, error)

	// FindClientInfoByRefKey finds the client of the given refKey.
	FindClientInfoByRefKey(ctx context.Context, refKey types.ClientRefKey, skipCache ...bool) (*ClientInfo, error)

	// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
	// after handling PushPull.
	UpdateClientInfoAfterPushPull(ctx context.Context, clientInfo *ClientInfo, docInfo *DocInfo) error

	// FindAttachedClientInfosByRefKey returns the attached client infos of the given document.
	FindAttachedClientInfosByRefKey(ctx context.Context, refKey types.DocRefKey) ([]*ClientInfo, error)

	// FindAttachedClientCountsByDocIDs returns the number of attached clients of the given documents as a map.
	FindAttachedClientCountsByDocIDs(ctx context.Context, projectID types.ID, docIDs []types.ID) (map[types.ID]int, error)

	// FindActiveClients finds active clients for deactivation checking.
	FindActiveClients(
		ctx context.Context,
		candidatesLimit int,
		lastClientID types.ID,
	) ([]*ClientInfo, types.ID, error)

	// FindCompactionCandidates finds documents that need compaction.
	FindCompactionCandidates(
		ctx context.Context,
		candidatesLimit int,
		compactionMinChanges int,
		lastDocID types.ID,
	) ([]*DocInfo, types.ID, error)

	// FindDocInfoByKey finds the document of the given key.
	FindDocInfoByKey(
		ctx context.Context,
		projectID types.ID,
		docKey key.Key,
	) (*DocInfo, error)

	// FindDocInfosByKeys finds the documents of the given keys.
	FindDocInfosByKeys(
		ctx context.Context,
		projectID types.ID,
		docKeys []key.Key,
	) ([]*DocInfo, error)

	// FindDocInfosByIDs finds the documents of the given IDs.
	FindDocInfosByIDs(
		ctx context.Context,
		projectID types.ID,
		docIDs []types.ID,
	) ([]*DocInfo, error)

	// FindOrCreateDocInfo finds the document or creates it if it does not exist.
	FindOrCreateDocInfo(
		ctx context.Context,
		clientRefKey types.ClientRefKey,
		docKey key.Key,
	) (*DocInfo, error)

	// FindDocInfoByRefKey finds the document of the given refKey.
	FindDocInfoByRefKey(
		ctx context.Context,
		refKey types.DocRefKey,
	) (*DocInfo, error)

	// UpdateDocInfoStatusToRemoved updates the document status to removed.
	UpdateDocInfoStatusToRemoved(
		ctx context.Context,
		refKey types.DocRefKey,
	) error

	// UpdateDocInfoSchema updates the document schema.
	UpdateDocInfoSchema(
		ctx context.Context,
		refKey types.DocRefKey,
		schemaKey string,
	) error

	// GetDocumentsCount returns the number of documents in the given project.
	GetDocumentsCount(ctx context.Context, projectID types.ID) (int64, error)

	// GetClientsCount returns the number of active clients in the given project.
	GetClientsCount(ctx context.Context, projectID types.ID) (int64, error)

	// CreateChangeInfos stores the given changes then updates the given docInfo.
	CreateChangeInfos(
		ctx context.Context,
		docRefKey types.DocRefKey,
		cpBeforePush change.Checkpoint,
		changes []*ChangeInfo,
		isRemoved bool,
	) (*DocInfo, change.Checkpoint, error)

	// CompactChangeInfos stores the given compacted changes then updates the docInfo.
	CompactChangeInfos(
		ctx context.Context,
		docInfo *DocInfo,
		lastServerSeq int64,
		changes []*change.Change,
	) error

	// FindLatestChangeInfoByActor returns the latest change created by given actorID.
	FindLatestChangeInfoByActor(
		ctx context.Context,
		docRefKey types.DocRefKey,
		actorID types.ID,
		serverSeq int64,
	) (*ChangeInfo, error)

	// FindChangesBetweenServerSeqs returns the changes between two server sequences.
	FindChangesBetweenServerSeqs(
		ctx context.Context,
		docRefKey types.DocRefKey,
		from int64,
		to int64,
	) ([]*change.Change, error)

	// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docRefKey types.DocRefKey,
		from int64,
		to int64,
	) ([]*ChangeInfo, error)

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(
		ctx context.Context,
		docRefKey types.DocRefKey,
		doc *document.InternalDocument,
	) error

	// FindSnapshotInfo return the snapshot by the given DocRefKey and serverSeq.
	FindSnapshotInfo(
		ctx context.Context,
		refKey types.DocRefKey,
		serverSeq int64,
	) (*SnapshotInfo, error)

	// FindClosestSnapshotInfo finds the closest snapshot info in a given serverSeq.
	FindClosestSnapshotInfo(
		ctx context.Context,
		docRefKey types.DocRefKey,
		serverSeq int64,
		includeSnapshot bool,
	) (*SnapshotInfo, error)

	// UpdateMinVersionVector updates the version vector of the given client
	// and returns the minimum version vector of all clients.
	UpdateMinVersionVector(
		ctx context.Context,
		clientInfo *ClientInfo,
		docRefKey types.DocRefKey,
		vector time.VersionVector,
	) (time.VersionVector, error)

	// GetMinVersionVector returns the minimum version vector of all clients.
	GetMinVersionVector(
		ctx context.Context,
		docRefKey types.DocRefKey,
		vector time.VersionVector,
	) (time.VersionVector, error)

	// FindDocInfosByPaging returns the documentInfos of the given paging.
	FindDocInfosByPaging(
		ctx context.Context,
		projectID types.ID,
		paging types.Paging[types.ID],
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
		docRefKey types.DocRefKey,
		excludeClientID types.ID,
	) (bool, error)

	// CreateSchemaInfo creates a new schema.
	CreateSchemaInfo(
		ctx context.Context,
		projectID types.ID,
		name string,
		version int,
		body string,
		rules []types.Rule,
	) (*SchemaInfo, error)

	// GetSchemaInfo retrieves a schema by its ID.
	GetSchemaInfo(
		ctx context.Context,
		projectID types.ID,
		name string,
		version int,
	) (*SchemaInfo, error)

	// GetSchemaInfos retrieves all versions of a schema by its name.
	GetSchemaInfos(
		ctx context.Context,
		projectID types.ID,
		name string,
	) ([]*SchemaInfo, error)

	// ListSchemaInfos lists all schemas in the project.
	ListSchemaInfos(
		ctx context.Context,
		projectID types.ID,
	) ([]*SchemaInfo, error)

	// RemoveSchemaInfo removes a schema by its ID.
	RemoveSchemaInfo(
		ctx context.Context,
		projectID types.ID,
		name string,
		version int,
	) error

	// PurgeDocument purges the given document.
	PurgeDocument(
		ctx context.Context,
		docRefKey types.DocRefKey,
	) (map[string]int64, error)

	// IsSchemaAttached returns true if the schema is being used by any documents.
	IsSchemaAttached(
		ctx context.Context,
		projectID types.ID,
		schema string,
	) (bool, error)

	// CreateWebhookLog creates a new webhook log.
	CreateWebhookLog(ctx context.Context, webhookLog *types.WebhookLogInfo) error

	// ListWebhookLogs returns webhook logs for a project.
	ListWebhookLogs(
		ctx context.Context,
		projectID types.ID,
		webhookType string,
		pageSize int,
		offset int,
	) ([]*types.WebhookLogInfo, error)
}
