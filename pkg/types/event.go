package types

type EventType string

const (
	DocumentChangeEvent    EventType = "document-change"
	DocumentWatchedEvent             = "document-watched"
	DocumentUnwatchedEvent           = "document-unwatched"
)
