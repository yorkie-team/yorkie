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

func NewArrayProxy(ctx *change.Context, array *json.Array) *ArrayProxy {
	return &ArrayProxy{
		Array:   array,
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
		return NewArrayProxy(p.context, json.NewArray(json.NewRGA(), ticket))
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
	proxy := creator(ticket)
	value := toOriginal(proxy)

	p.context.Push(operation.NewAdd(
		p.Array.CreatedAt(),
		p.Array.LastCreatedAt(),
		value.Deepcopy(),
		ticket,
	))

	p.Add(value)
	p.context.RegisterElement(value)

	return proxy
}

func (p *ArrayProxy) Len() int {
	return p.Array.Len()
}
