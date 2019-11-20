package datatype

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Element represents JSON element.
type Element interface {
	Marshal() string
	CreatedAt() *time.Ticket
}
