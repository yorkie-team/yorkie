package types

// DocEventType represents the event that the Server delivers to the client.
type DocEventType string

const (
	// DocumentChangedEvent is an event indicating that document is being
	// modified by a change.
	DocumentChangedEvent DocEventType = "document-changed"

	// DocumentWatchedEvent is an event that occurs when document is watched
	// by other clients.
	DocumentWatchedEvent DocEventType = "document-watched"

	// DocumentUnwatchedEvent is an event that occurs when document is
	// unwatched by other clients.
	DocumentUnwatchedEvent DocEventType = "document-unwatched"
)
