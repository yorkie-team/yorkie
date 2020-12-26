package db

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/yorkie/types"
)

var (
	// ErrInvalidID is returned when the given ID is not ObjectID.
	ErrInvalidID = errors.New("invalid ID")

	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.New("client not found")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.New("document not found")
)

// DB represents database which reads or saves Yorkie data.
type DB interface {
	// Close all resources of this database.
	Close() error

	// ActivateClient activates the client of the given key.
	ActivateClient(ctx context.Context, key string) (*types.ClientInfo, error)

	// DeactivateClient deactivates the client of the given ID.
	DeactivateClient(ctx context.Context, clientID string) (*types.ClientInfo, error)

	// FindClientInfoByID finds the client of the given ID.
	FindClientInfoByID(ctx context.Context, clientID string) (*types.ClientInfo, error)

	// UpdateClientInfoAfterPushPull updates the client from the given clientInfo
	// after handling PushPull.
	UpdateClientInfoAfterPushPull(ctx context.Context, clientInfo *types.ClientInfo, docInfo *types.DocInfo) error

	// FindDocInfoByKey finds the document of the given key. If the
	// createDocIfNotExist condition is true, create the document if it does not
	// exist.
	FindDocInfoByKey(
		ctx context.Context,
		clientInfo *types.ClientInfo,
		bsonDocKey string,
		createDocIfNotExist bool,
	) (*types.DocInfo, error)

	// CreateChangeInfos stores the given changes.
	CreateChangeInfos(ctx context.Context, docID primitive.ObjectID, changes []*change.Change) error

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(ctx context.Context, docID primitive.ObjectID, doc *document.InternalDocument) error

	// UpdateDocInfo updates the given document.
	UpdateDocInfo(ctx context.Context, docInfo *types.DocInfo) error

	// FindChangeInfosBetweenServerSeqs returns the changes between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docID primitive.ObjectID,
		from uint64,
		to uint64,
	) ([]*change.Change, error)

	// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
	// and returns the min synced ticket.
	UpdateAndFindMinSyncedTicket(
		ctx context.Context,
		clientInfo *types.ClientInfo,
		docID primitive.ObjectID,
		serverSeq uint64,
	) (*time.Ticket, error)

	// FindLastSnapshotInfo finds the last snapshot of the given document.
	FindLastSnapshotInfo(ctx context.Context, docID primitive.ObjectID) (*types.SnapshotInfo, error)
}
