package datatype

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Element interface {
	Marshal() string
	CreatedAt() *time.Ticket
}
