package datatype

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

// Text is an extended data type for the contents of a text editor.
type Text struct {
	createdAt *time.Ticket
}

// NewText creates a new instance of Text.
func NewText(createdAt *time.Ticket) *Text{
	return &Text{
		createdAt: createdAt,
	}
}

func (t *Text) Marshal() string {
	log.Logger.Warn("unimplemented")
	return ""
}

// CreatedAt returns the creation time of this Text.
func (t *Text) CreatedAt() *time.Ticket {
	return t.createdAt
}

func (t *Text) Edit(from, to int, content string) *Text {
	log.Logger.Warn("unimplemented")
	return t
}
