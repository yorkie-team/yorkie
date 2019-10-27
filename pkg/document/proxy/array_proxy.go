package proxy

import (
	"github.com/hackerwins/rottie/pkg/document/change"
	"github.com/hackerwins/rottie/pkg/document/json"
	"github.com/hackerwins/rottie/pkg/document/json/datatype"
	"github.com/hackerwins/rottie/pkg/document/operation"
	"github.com/hackerwins/rottie/pkg/document/time"
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
		case *json.Primitive:
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

func (p *ArrayProxy) ToOriginal() datatype.Element {
	return p.Array
}

func (p *ArrayProxy) AddString(v string) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) datatype.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) addInternal(
	creator func(ticket *time.Ticket) datatype.Element,
) datatype.Element {
	ticket := p.context.IssueTimeTicket()
	value := creator(ticket)
	p.Add(value)

	p.context.Push(operation.NewAdd(
		toOriginal(value),
		p.Array.CreatedAt(),
		p.Array.LastCreatedAt(),
		ticket,
	))

	return value
}
