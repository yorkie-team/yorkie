package types

import (
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/resource"
)

// DocumentSummary represents a summary of document.
type DocumentSummary struct {
	// ID is the unique identifier of the document.
	ID ID

	// Key is the key of the document.
	Key key.Key

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time

	// AccessedAt is the time when the document is accessed.
	AccessedAt time.Time

	// UpdatedAt is the time when the document is updated.
	UpdatedAt time.Time

	// Root is the root object of the document.
	Root string

	// AttachedClients is the count of clients attached to the document.
	AttachedClients int

	// DocSize represents the size of a document in bytes.
	DocSize resource.DocSize

	// SchemaKey is the key of the schema of the document.
	SchemaKey string

	// Presences is the presence information of all clients.
	Presences map[string]innerpresence.Presence
}
