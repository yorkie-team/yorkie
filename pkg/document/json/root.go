package json

import (
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
	elementMapByCreatedAt map[string]Element
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	elementMap := make(map[string]Element)
	r := &Root{
		object:                root,
		elementMapByCreatedAt: elementMap,
	}

	r.RegisterElement(root)

	descendants := make(chan Element)
	go func() {
		root.Descendants(descendants)
		close(descendants)
	}()
	for descendant := range descendants {
		r.RegisterElement(descendant)
	}

	return r
}

// Object returns the root object of the JSON.
func (r *Root) Object() *Object {
	return r.object
}

// FindByCreatedAt returns the element of given creation time.
func (r *Root) FindByCreatedAt(createdAt *time.Ticket) Element {
	return r.elementMapByCreatedAt[createdAt.Key()]
}

// RegisterElement registers the given element to hash table.
func (r *Root) RegisterElement(elem Element) {
	r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
}

func (r *Root) Deepcopy() *Root {
	return NewRoot(r.object.Deepcopy().(*Object))
}
