package types

// DocEventType represents the event that the Agent delivers to the client.
type DocEventType string

const (
	// DocumentsChangedEvent is an event indicating that documents are being
	// modified by a change.
	DocumentsChangedEvent DocEventType = "documents-changed"

	// DocumentsWatchedEvent is an event that occurs when documents are watched
	// by other clients.
	DocumentsWatchedEvent DocEventType = "documents-watched"

	// DocumentsUnwatchedEvent is an event that occurs when documents are
	// unwatched by other clients.
	DocumentsUnwatchedEvent DocEventType = "documents-unwatched"

	// MetadataChangedEvent is an event indicating that metadata is changed.
	MetadataChangedEvent DocEventType = "metadata-changed"
)
