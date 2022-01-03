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

package db

import (
	"context"
	"errors"
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.New("client not found")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.New("document not found")

	// ErrConflictOnUpdate is returned when a conflict occurs during update.
	ErrConflictOnUpdate = errors.New("conflict on update")
)

// DB represents database which reads or saves Yorkie data.
type DB interface {
	// Close all resources of this database.
	Close() error

	// ActivateClient activates the client of the given key.
	ActivateClient(ctx context.Context, key string) (*ClientInfo, error)

	// DeactivateClient deactivates the client of the given ID.
	DeactivateClient(ctx context.Context, clientID ID) (*ClientInfo, error)

	// FindClientInfoByID finds the client of the given ID.
	FindClientInfoByID(ctx context.Context, clientID ID) (*ClientInfo, error)

	// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
	// after handling PushPull.
	UpdateClientInfoAfterPushPull(ctx context.Context, clientInfo *ClientInfo, docInfo *DocInfo) error

	// FindDeactivateCandidates finds the housekeeping candidates.
	FindDeactivateCandidates(
		ctx context.Context,
		deactivateThreshold gotime.Duration,
		candidatesLimit int,
	) ([]*ClientInfo, error)

	// FindDocInfoByKey finds the document of the given key. If the
	// createDocIfNotExist condition is true, create the document if it does not
	// exist.
	FindDocInfoByKey(
		ctx context.Context,
		clientInfo *ClientInfo,
		bsonDocKey string,
		createDocIfNotExist bool,
	) (*DocInfo, error)

	// CreateChangeInfos stores the given changes then updates the given docInfo.
	CreateChangeInfos(
		ctx context.Context,
		docInfo *DocInfo,
		initialServerSeq uint64,
		changes []*change.Change,
	) error

	// FindChangesBetweenServerSeqs returns the changes between two server sequences.
	FindChangesBetweenServerSeqs(
		ctx context.Context,
		docID ID,
		from uint64,
		to uint64,
	) ([]*change.Change, error)

	// FindChangeInfosBetweenServerSeqs returns the changeInfos between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docID ID,
		from uint64,
		to uint64,
	) ([]*ChangeInfo, error)

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(ctx context.Context, docID ID, doc *document.InternalDocument) error

	// FindLastSnapshotInfo finds the last snapshot of the given document.
	FindLastSnapshotInfo(ctx context.Context, docID ID) (*SnapshotInfo, error)

	// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
	// and returns the min synced ticket.
	UpdateAndFindMinSyncedTicket(
		ctx context.Context,
		clientInfo *ClientInfo,
		docID ID,
		serverSeq uint64,
	) (*time.Ticket, error)

	// UpdateSyncedSeq updates the syncedSeq of the given client.
	UpdateSyncedSeq(
		ctx context.Context,
		clientInfo *ClientInfo,
		docID ID,
		serverSeq uint64,
	) error
}
