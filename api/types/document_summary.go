package types

import (
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/key"
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

	// Snapshot is the string representation of the document.
	Snapshot string
}
