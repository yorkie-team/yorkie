package datatype

import "github.com/hackerwins/rottie/pkg/document/time"

type Element interface {
	Marshal() string
	CreatedAt() *time.Ticket
}
