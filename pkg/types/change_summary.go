package types

import "github.com/yorkie-team/yorkie/pkg/document/change"

// ChangeSummary represents a summary of change.
type ChangeSummary struct {
	// ID is the unique identifier of the change.
	ID change.ID

	// Message is the message of the change.
	Message string

	// Snapshot is the snapshot of the document.
	Snapshot string
}
