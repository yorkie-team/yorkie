package types

type EventType string

const (
	DocumentsChangeEvent    EventType = "documents-change"
	DocumentsWatchedEvent   EventType = "documents-watched"
	DocumentsUnwatchedEvent EventType = "documents-unwatched"
)
