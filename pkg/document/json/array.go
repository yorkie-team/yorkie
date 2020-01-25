package json

import (
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Array represents JSON array data structure including logical clock.
// Array implements Element interface.
type Array struct {
	elements  *RGA
	createdAt *time.Ticket
	deletedAt *time.Ticket
}

// NewArray creates a new instance of Array.
func NewArray(elements *RGA, createdAt *time.Ticket) *Array {
	return &Array{
		elements:  elements,
		createdAt: createdAt,
	}
}

// Add adds the given element at the last.
func (a *Array) Add(elem Element) *Array {
	a.elements.Add(elem)
	return a
}

// Get returns the element of the given index.
func (a *Array) Get(idx int) Element {
	return a.elements.Get(idx)
}

// Remove removes the element of the given index.
func (a *Array) Remove(idx int, deletedAt *time.Ticket) Element {
	removed := a.elements.Get(idx)
	if removed != nil {
		a.elements.RemoveByCreatedAt(removed.CreatedAt(), deletedAt)
	}

	return removed
}

// Nodes returns an array of elements contained in this RGA.
func (a *Array) Elements() []Element {
	var elements []Element
	for _, node := range a.elements.Nodes() {
		if node.isDeleted() {
			continue
		}
		elements = append(elements, node.elem)
	}

	return elements
}

// Marshal returns the JSON encoding of this Array.
func (a *Array) Marshal() string {
	return a.elements.Marshal()
}

// Deepcopy copies itself deeply.
func (a *Array) Deepcopy() Element {
	elements := NewRGA()

	for _, node := range a.elements.Nodes() {
		elements.Add(node.elem.Deepcopy())
	}

	array := NewArray(elements, a.createdAt)
	array.deletedAt = a.deletedAt
	return array
}

// CreatedAt returns the creation time of this Array.
func (a *Array) CreatedAt() *time.Ticket {
	return a.createdAt
}

// DeletedAt returns the deletion time of this Array.
func (a *Array) DeletedAt() *time.Ticket {
	return a.deletedAt
}

// Delete deletes this element.
func (a *Array) Delete(deletedAt *time.Ticket) {
	a.deletedAt = deletedAt
}

// LastCreatedAt returns the creation time of the last element.
func (a *Array) LastCreatedAt() *time.Ticket {
	return a.elements.LastCreatedAt()
}

// InsertAfter inserts the given element after the given previous element.
func (a *Array) InsertAfter(prevCreatedAt *time.Ticket, element Element) {
	a.elements.InsertAfter(prevCreatedAt, element)
}

// RemoveByCreatedAt removes the given element.
func (a *Array) RemoveByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element {
	return a.elements.RemoveByCreatedAt(createdAt, deletedAt)
}

// Len returns length of this Array.
func (a *Array) Len() int {
	return a.elements.Len()
}

func (a *Array) Descendants(descendants chan Element) {
	for _, node := range a.elements.Nodes() {
		switch elem := node.elem.(type) {
		case *Object:
			elem.Descendants(descendants)
		case *Array:
			elem.Descendants(descendants)
		}
		descendants <- node.elem
	}
}
