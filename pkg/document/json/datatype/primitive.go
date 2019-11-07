package datatype

import (
	"fmt"

	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Primitive struct {
	value     string
	createdAt *time.Ticket
}

func NewPrimitive(value string, createdAt *time.Ticket) *Primitive {
	return &Primitive{
		value:     value,
		createdAt: createdAt,
	}
}

func (p *Primitive) Marshal() string {
	return fmt.Sprintf("\"%s\"", p.value)
}

func (p *Primitive) CreatedAt() *time.Ticket {
	return p.createdAt
}

func (p *Primitive) Value() []byte {
	return []byte(p.value)
}
