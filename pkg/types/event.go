package types

// EventType represents the event that the Agent delivers to the client.
type EventType string

const (
	// DocumentsChangeEvent is an event indicating that documents are being
	// modified by a change.
	DocumentsChangeEvent EventType = "documents-change"

	// DocumentsWatchedEvent is an event that occurs when documents are watched
	// by other clients.
	DocumentsWatchedEvent EventType = "documents-watched"

	// DocumentsUnwatchedEvent is an event that occurs when documents are
	// unwatched by other clients.
	DocumentsUnwatchedEvent EventType = "documents-unwatched"
)
