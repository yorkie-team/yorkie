package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Root struct {
	object                *Object
	elementMapByCreatedAt map[string]datatype.Element
}

func NewRoot() *Root {
	root := NewObject(datatype.NewRHT(), time.InitialTicket)
	elementMap := make(map[string]datatype.Element)
	elementMap[root.CreatedAt().Key()] = root

	return &Root{
		object:                root,
		elementMapByCreatedAt: elementMap,
	}
}

func (r *Root) Object() *Object {
	return r.object
}

func (r *Root) FindByCreatedAt(ticket *time.Ticket) datatype.Element {
	return r.elementMapByCreatedAt[ticket.Key()]
}

func (r *Root) RegisterElement(elem datatype.Element) {
	r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
}
