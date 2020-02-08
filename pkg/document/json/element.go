package json

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Element represents JSON element.
type Element interface {
	// Marshal returns the JSON encoding of this element.
	Marshal() string

	// Deepcopy copies itself deeply.
	Deepcopy() Element

	// CreatedAt returns the creation time of this element.
	CreatedAt() *time.Ticket

	// DeletedAt returns the deletion time of this element.
	DeletedAt() *time.Ticket

	// Delete deletes this element.
	Delete(*time.Ticket)
}
