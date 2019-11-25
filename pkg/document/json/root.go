package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

// Root is a structure represents the root of JSON. It has a hash table of
// all JSON elements to find a specific element when appling remote changes
// received from agent.
//
// Every element has a unique time ticket at creation, which allows us to find
// a particular element.
type Root struct {
	object                *Object
	elementMapByCreatedAt map[string]datatype.Element
}

// NewRoot creates a new instance of Root.
func NewRoot() *Root {
	root := NewObject(datatype.NewRHT(), time.InitialTicket)
	elementMap := make(map[string]datatype.Element)
	elementMap[root.CreatedAt().Key()] = root

	return &Root{
		object:                root,
		elementMapByCreatedAt: elementMap,
	}
}

// Object returns the root object of the JSON.
func (r *Root) Object() *Object {
	return r.object
}

// FindByCreatedAt returns the element of given creation time.
func (r *Root) FindByCreatedAt(ticket *time.Ticket) datatype.Element {
	return r.elementMapByCreatedAt[ticket.Key()]
}

// RegisterElement registers the given element to hash table.
func (r *Root) RegisterElement(elem datatype.Element) {
	r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
}
