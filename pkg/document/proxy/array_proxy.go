package proxy

import (
	time2 "time"

	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type ArrayProxy struct {
	*json.Array
	context *change.Context
}

func ProxyArray(ctx *change.Context, root *json.Array) *ArrayProxy {
	return &ArrayProxy{
		Array:   root,
		context: ctx,
	}
}

func NewArrayProxy(
	ctx *change.Context,
	elements *json.RGA,
	createdAt *time.Ticket,
) *ArrayProxy {
	return &ArrayProxy{
		Array:   json.NewArray(elements, createdAt),
		context: ctx,
	}
}

func (p *ArrayProxy) AddBool(v bool) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddInteger(v int) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddLong(v int64) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddDouble(v float64) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddBytes(v []byte) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddDate(v time2.Time) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddString(v string) *ArrayProxy {
	p.addInternal(func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ArrayProxy) AddNewArray() *ArrayProxy {
	v := p.addInternal(func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewRGA(), ticket)
	})

	return v.(*ArrayProxy)
}

func (p *ArrayProxy) Remove(idx int) json.Element {
	removed := p.Array.Remove(idx)

	if removed != nil {
		ticket := p.context.IssueTimeTicket()
		p.context.Push(operation.NewRemove(
			p.CreatedAt(),
			removed.CreatedAt(),
			ticket,
		))
	}

	return removed
}

func (p *ArrayProxy) addInternal(
	creator func(ticket *time.Ticket) json.Element,
) json.Element {
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

func (p *ArrayProxy) Len() int {
	return p.Array.Len()
}
