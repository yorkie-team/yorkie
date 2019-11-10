package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Array struct {
	elements  *datatype.RGA
	createdAt *time.Ticket
}

func NewArray(elements *datatype.RGA, createdAt *time.Ticket) *Array {
	return &Array{
		elements:  elements,
		createdAt: createdAt,
	}
}

func (a *Array) Add(v datatype.Element) *Array {
	a.elements.Add(v)
	return a
}

func (a *Array) Elements() []datatype.Element {
	return a.elements.Elements()
}

func (a *Array) Marshal() string {
	return a.elements.Marshal()
}

func (a *Array) CreatedAt() *time.Ticket {
	return a.createdAt
}

func (a *Array) LastCreatedAt() *time.Ticket {
	return a.elements.LastCreatedAt()
}

func (a *Array) InsertAfter(prevCreatedAt *time.Ticket, element datatype.Element) {
	a.elements.InsertAfter(prevCreatedAt, element)
}

func (a *Array) Remove(createdAt *time.Ticket) datatype.Element {
	return a.elements.Remove(createdAt)
}
