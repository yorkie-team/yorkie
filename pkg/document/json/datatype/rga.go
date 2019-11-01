package datatype

import (
	"strings"

	"github.com/hackerwins/rottie/pkg/document/time"
)

// RGA is replicated growable array.
type RGA struct {
	elements []Element
}

func (a *RGA) Marshal() string {
	sb := strings.Builder{}

	sb.WriteString("[")
	for idx, elem := range a.elements {
		sb.WriteString(elem.Marshal())
		if len(a.elements)-1 != idx {
			sb.WriteString(",")
		}
	}

	sb.WriteString("]")

	return sb.String()
}

func (a *RGA) Add(e Element) {
	a.elements = append(a.elements, e)
}

func (a *RGA) Elements() []Element {
	return a.elements
}

func (a *RGA) LastCreatedAt() *time.Ticket {
	size := len(a.elements)
	if size == 0 {
		return nil
	}
	last := a.elements[size-1]
	return last.CreatedAt()
}

func NewRGA() *RGA {
	return &RGA{}
}
