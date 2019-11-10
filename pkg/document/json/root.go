package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Root struct {
	object                  *Object
	elementMapByCreatedAt   map[string]datatype.Element
	tombstoneMapByCreatedAt map[string]datatype.Element
}

func NewRoot() *Root {
	root := NewObject(datatype.NewRHT(), time.InitialTicket)
	elementMap := make(map[string]datatype.Element)
	tombstoneMap := make(map[string]datatype.Element)
	elementMap[root.CreatedAt().Key()] = root

	return &Root{
		object:                  root,
		elementMapByCreatedAt:   elementMap,
		tombstoneMapByCreatedAt: tombstoneMap,
	}
}

func (r *Root) Object() *Object {
	return r.object
}

func (r *Root) FindByCreatedAt(ticket *time.Ticket) datatype.Element {
	return r.elementMapByCreatedAt[ticket.Key()]
}

func (r *Root) RegisterElement(element datatype.Element) {
	r.elementMapByCreatedAt[element.CreatedAt().Key()] = element
}

func (r *Root) DeregisterElement(createdAt *time.Ticket) {
	elem, ok := r.elementMapByCreatedAt[createdAt.Key()]
	if ok {
		delete(r.elementMapByCreatedAt, createdAt.Key())
		r.tombstoneMapByCreatedAt[createdAt.Key()] = elem
	}
}

func traverse(element datatype.Element, f func(elem datatype.Element)) {
	f(element)

	switch elem := element.(type) {
	case *Object:
		for _, child := range elem.Members() {
			traverse(child, f)
		}
	case *Array:
		for _, child := range elem.Elements() {
			traverse(child, f)
		}
	case *datatype.Primitive:
	default:
		panic("fail to ")
	}
}

func GetDescendants(root datatype.Element) []datatype.Element {
	var descendants []datatype.Element
	traverse(root, func(elem datatype.Element) {
		descendants = append(descendants, elem)
	})
	return descendants
}