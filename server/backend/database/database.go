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

package database

import (
	"context"
	"errors"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrProjectAlreadyExists is returned when the project already exists.
	ErrProjectAlreadyExists = errors.New("project already exists")

	// ErrProjectNotFound is returned when the project is not found.
	ErrProjectNotFound = errors.New("project not found")

	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.New("client not found")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.New("document not found")

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
	FindProjectInfoByPublicKey(ctx context.Context, publicKey string) (*ProjectInfo, error)

	// FindProjectInfoByName returns a project by the given name.
	FindProjectInfoByName(ctx context.Context, name string) (*ProjectInfo, error)

	// FindProjectInfoByID returns a project by the given id.
	FindProjectInfoByID(ctx context.Context, id types.ID) (*ProjectInfo, error)

	// EnsureDefaultProjectInfo ensures that the default project exists.
	EnsureDefaultProjectInfo(ctx context.Context) (*ProjectInfo, error)

	// CreateProjectInfo creates a new project.
	CreateProjectInfo(ctx context.Context, name string) (*ProjectInfo, error)

	// ListProjectInfos returns all projects.
	ListProjectInfos(ctx context.Context) ([]*ProjectInfo, error)

	// UpdateProjectInfo updates the project.
	UpdateProjectInfo(ctx context.Context, id types.ID, fields *types.UpdatableProjectFields) (*ProjectInfo, error)

	// ActivateClient activates the client of the given key.
	ActivateClient(ctx context.Context, projectID types.ID, key string) (*ClientInfo, error)

	// DeactivateClient deactivates the client of the given ID.
	DeactivateClient(ctx context.Context, projectID, clientID types.ID) (*ClientInfo, error)

	// FindClientInfoByID finds the client of the given ID.
	FindClientInfoByID(ctx context.Context, projectID, clientID types.ID) (*ClientInfo, error)

	// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
	// after handling PushPull.
	UpdateClientInfoAfterPushPull(ctx context.Context, clientInfo *ClientInfo, docInfo *DocInfo) error

	// FindDeactivateCandidates finds the housekeeping candidates.
	FindDeactivateCandidates(
		ctx context.Context,
		deactivateThreshold gotime.Duration,
		candidatesLimit int,
	) ([]*ClientInfo, error)

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
		clientID types.ID,
		docKey key.Key,
		createDocIfNotExist bool,
	) (*DocInfo, error)

	// FindDocInfoByID finds the document of the given ID.
	FindDocInfoByID(
		ctx context.Context,
		id types.ID,
	) (*DocInfo, error)

	// CreateChangeInfos stores the given changes then updates the given docInfo.
	CreateChangeInfos(
		ctx context.Context,
		projectID types.ID,
		docInfo *DocInfo,
		initialServerSeq uint64,
		changes []*change.Change,
	) error

	// FindChangesBetweenServerSeqs returns the changes between two server sequences.
	FindChangesBetweenServerSeqs(
		ctx context.Context,
		docID types.ID,
		from uint64,
		to uint64,
	) ([]*change.Change, error)

	// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docID types.ID,
		from uint64,
		to uint64,
	) ([]*ChangeInfo, error)

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(ctx context.Context, docID types.ID, doc *document.InternalDocument) error

	// FindClosestSnapshotInfo finds the closest snapshot info in a given serverSeq.
	FindClosestSnapshotInfo(ctx context.Context, docID types.ID, serverSeq uint64) (*SnapshotInfo, error)

	// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
	// and returns the min synced ticket.
	UpdateAndFindMinSyncedTicket(
		ctx context.Context,
		clientInfo *ClientInfo,
		docID types.ID,
		serverSeq uint64,
	) (*time.Ticket, error)

	// UpdateSyncedSeq updates the syncedSeq of the given client.
	UpdateSyncedSeq(
		ctx context.Context,
		clientInfo *ClientInfo,
		docID types.ID,
		serverSeq uint64,
	) error

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
		paging types.Paging[types.ID],
	) ([]*DocInfo, error)
}
