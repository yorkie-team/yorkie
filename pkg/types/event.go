package types

type EventType string

const (
	DocumentsChangeEvent    EventType = "documents-change"
	DocumentsWatchedEvent             = "documents-watched"
	DocumentsUnwatchedEvent           = "documents-unwatched"
)
