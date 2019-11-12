package proxy

import (
	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/json/datatype"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type ArrayProxy struct {
	*json.Array
	context *change.Context
}

func ProxyArray(ctx *change.Context, root *json.Array) *ArrayProxy {
	elements := datatype.NewRGA()

	for _, val := range root.Elements() {
		switch elem := val.(type) {
		case *json.Object:
			elements.Add(ProxyObject(ctx, elem))
		case *json.Array:
			elements.Add(ProxyArray(ctx, elem))
		case *datatype.Primitive:
			elements.Add(elem)
		}
	}

	return NewArrayProxy(ctx, elements, root.CreatedAt())
}

func NewArrayProxy(
	ctx *change.Context,
	elements *datatype.RGA,
	createdAt *time.Ticket,
) *ArrayProxy {
	return &ArrayProxy{
		Array:   json.NewArray(elements, createdAt),
		context: ctx,
	}
}

func (p *ArrayProxy) AddString(v string) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) datatype.Element {
		return datatype.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddNewArray() *ArrayProxy {
	v := p.addInternal(func(ticket *time.Ticket) datatype.Element {
		return NewArrayProxy(p.context, datatype.NewRGA(), ticket)
	})

	return v.(*ArrayProxy)
}

func (p *ArrayProxy) addInternal(
	creator func(ticket *time.Ticket) datatype.Element,
) datatype.Element {
	ticket := p.context.IssueTimeTicket()
	value := creator(ticket)

	p.context.Push(operation.NewAdd(
		p.Array.CreatedAt(),
		p.Array.LastCreatedAt(),
		toOriginal(value),
		ticket,
	))

	p.Add(value)

	return value
}

func (p *ArrayProxy) Remove(idx int) {

}

