package types

import "github.com/yorkie-team/yorkie/pkg/document/time"

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type       DocEventType
	Publisher  *time.ActorID
	DocumentID ID
}

// BroadcastEvent represents events that are delievered to subscribers.
type BroadcastEvent struct {
	Type      string
	Publisher *time.ActorID
	Payload   []byte
}

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
