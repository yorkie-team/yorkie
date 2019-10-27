package json

import (
	"fmt"

	"github.com/hackerwins/rottie/pkg/document/time"
)

type Primitive struct {
	value     interface{}
	createdAt *time.Ticket
}

func NewPrimitive(value interface{}, createdAt *time.Ticket) *Primitive {
	return &Primitive{
		value:     value,
		createdAt: createdAt,
	}
}

func (p *Primitive) Marshal() string {
	switch v := p.value.(type) {
	case string:
		return fmt.Sprintf("\"%s\"", v)
	default:
		return fmt.Sprintf("%s", v)
	}
}

func (p *Primitive) CreatedAt() *time.Ticket {
	return p.createdAt
}
