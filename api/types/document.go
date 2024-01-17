package types

import (
	"time"
)

// Document is a structure representing information of the document.
type Document struct {
	// ID is the unique ID of the document.
	ID ID `json:"_id"`

	// ProjectID is the ID of the project that the document belongs to.
	ProjectID ID `json:"project_id"`

	// Key is the key of the document.
	Key string `json:"key"`

	// ServerSeq is the sequence number of the last change of the document on the server.
	ServerSeq int64 `json:"server_seq"`

	// Owner is the owner(ID of the client) of the document.
	Owner ID `json:"owner"`

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time `json:"created_at"`
}
