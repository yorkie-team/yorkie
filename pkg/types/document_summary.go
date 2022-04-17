package types

import (
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// DocumentSummary represents a summary of change.
type DocumentSummary struct {
	// ID is the unique identifier of the change.
	ID string

	// Key is the key of the document.
	Key key.Key

	// Snapshot is the string representation of the document.
	Snapshot string
}
