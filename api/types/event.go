package types

// DocEventType represents the event that the Server delivers to the client.
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

	// PresenceChangedEvent is an event indicating that presence is changed.
	PresenceChangedEvent DocEventType = "presence-changed"
)
