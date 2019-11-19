package datatype

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
	"github.com/hackerwins/yorkie/pkg/log"
)

type Text struct {
	createdAt *time.Ticket
}

func NewText(createdAt *time.Ticket) *Text{
	return &Text{
		createdAt: createdAt,
	}
}

func (t *Text) Marshal() string {
	log.Logger.Warn("unimplemented")
	return ""
}

func (t *Text) CreatedAt() *time.Ticket {
	return t.createdAt
}

func (t *Text) Edit(from, to int, content string) *Text {
	log.Logger.Warn("unimplemented")
	return t
}
