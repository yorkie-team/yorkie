package db

import (
	"context"
	"encoding/hex"

	"errors"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrInvalidID is returned when the given ID is not ObjectID.
	ErrInvalidID = errors.New("invalid ID")

	// ErrClientNotFound is returned when the client could not be found.
	ErrClientNotFound = errors.New("client not found")

	// ErrDocumentNotFound is returned when the document could not be found.
	ErrDocumentNotFound = errors.New("document not found")

	// ErrConflictOnUpdate is returned when a conflict occurs during update.
	ErrConflictOnUpdate = errors.New("conflict on update")
)

// ID represents ID of entity.
type ID string

// String returns a string representation of this ID.
func (id ID) String() string {
	return string(id)
}

// Bytes returns bytes of decoded hexadecimal string representation of this ID.
func (id ID) Bytes() []byte {
	decoded, err := hex.DecodeString(id.String())
	if err != nil {
		return nil
	}
	return decoded
}

// IDFromBytes returns ID represented by the encoded hexadecimal string from bytes.
func IDFromBytes(bytes []byte) ID {
	return ID(hex.EncodeToString(bytes))
}

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

	// FindDocInfoByKey finds the document of the given key. If the
	// createDocIfNotExist condition is true, create the document if it does not
	// exist.
	FindDocInfoByKey(
		ctx context.Context,
		clientInfo *ClientInfo,
		bsonDocKey string,
		createDocIfNotExist bool,
	) (*DocInfo, error)

	// StoreChangeInfos stores the given changes then updates the given docInfo.
	StoreChangeInfos(
		ctx context.Context,
		docInfo *DocInfo,
		initialServerSeq uint64,
		changes []*change.Change,
	) error

	// CreateSnapshotInfo stores the snapshot of the given document.
	CreateSnapshotInfo(ctx context.Context, docID ID, doc *document.InternalDocument) error

	// FindChangeInfosBetweenServerSeqs returns the changes between two server sequences.
	FindChangeInfosBetweenServerSeqs(
		ctx context.Context,
		docID ID,
		from uint64,
		to uint64,
	) ([]*change.Change, error)

	// UpdateAndFindMinSyncedTicket updates the given serverSeq of the given client
	// and returns the min synced ticket.
	UpdateAndFindMinSyncedTicket(
		ctx context.Context,
		clientInfo *ClientInfo,
		docID ID,
		serverSeq uint64,
	) (*time.Ticket, error)

	// FindLastSnapshotInfo finds the last snapshot of the given document.
	FindLastSnapshotInfo(ctx context.Context, docID ID) (*SnapshotInfo, error)
}
