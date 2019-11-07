package json

import (
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type Root struct {
	object       *Object
	elementMap   map[string]datatype.Element
	tombstoneMap map[string]datatype.Element
}

func NewRoot() *Root {
	root := NewObject(datatype.NewRHT(), time.InitialTicket)
	elementMap := make(map[string]datatype.Element)
	tombstoneMap := make(map[string]datatype.Element)
	elementMap[root.CreatedAt().Key()] = root

	return &Root{
		object:       root,
		elementMap:   elementMap,
		tombstoneMap: tombstoneMap,
	}
}

func (r *Root) Object() *Object {
	return r.object
}

func (r *Root) FindByCreatedAt(ticket *time.Ticket) datatype.Element {
	return r.elementMap[ticket.Key()]
}

func (r *Root) RegisterElement(element datatype.Element) {
	r.elementMap[element.CreatedAt().Key()] = element
}
