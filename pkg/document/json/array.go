package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Array represents JSON array data structure including logical clock.
// Array implements Element interface.
type Array struct {
	elements  *datatype.RGA
	createdAt *time.Ticket
}

// NewArray creates a new instance of Array.
func NewArray(elements *datatype.RGA, createdAt *time.Ticket) *Array {
	return &Array{
		elements:  elements,
		createdAt: createdAt,
	}
}

// Add adds the given element at the last.
func (a *Array) Add(v datatype.Element) *Array {
	a.elements.Add(v)
	return a
}

// Get returns the element of the given index.
func (a *Array) Get(idx int) datatype.Element {
	return a.elements.Get(idx)
}

// Remove removes the element of the given index.
func (a *Array) Remove(idx int) datatype.Element {
	removed := a.elements.Get(idx)
	if removed != nil {
		a.elements.RemoveByCreatedAt(removed.CreatedAt())
	}

	return removed
}

// Elements returns an array of elements contained in this RGA.
func (a *Array) Elements() []datatype.Element {
	return a.elements.Elements()
}

// Marshal returns the JSON encoding of this Array.
func (a *Array) Marshal() string {
	return a.elements.Marshal()
}

// Deepcopy copies itself deeply.
func (a *Array) Deepcopy() datatype.Element {
	elements := datatype.NewRGA()

	for _, elem := range a.elements.Elements() {
		elements.Add(elem.Deepcopy())
	}

	return NewArray(elements, a.createdAt)
}

// CreatedAt returns the creation time of this Array.
func (a *Array) CreatedAt() *time.Ticket {
	return a.createdAt
}

// LastCreatedAt returns the creation time of the last element.
func (a *Array) LastCreatedAt() *time.Ticket {
	return a.elements.LastCreatedAt()
}

// InsertAfter inserts the given element after the given previous element.
func (a *Array) InsertAfter(prevCreatedAt *time.Ticket, element datatype.Element) {
	a.elements.InsertAfter(prevCreatedAt, element)
}

// RemoveByCreatedAt removes the given element.
func (a *Array) RemoveByCreatedAt(createdAt *time.Ticket) datatype.Element {
	return a.elements.RemoveByCreatedAt(createdAt)
}

// Len returns length of this Array.
func (a *Array) Len() int {
	return a.elements.Len()
}
