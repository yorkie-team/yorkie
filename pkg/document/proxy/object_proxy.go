package proxy

import (
	time2 "time"

	"github.com/hackerwins/yorkie/pkg/document/change"
	"github.com/hackerwins/yorkie/pkg/document/json"
	"github.com/hackerwins/yorkie/pkg/document/operation"
	"github.com/hackerwins/yorkie/pkg/document/time"
)

type ObjectProxy struct {
	*json.Object
	context *change.Context
}

// ProxyObject creates an ObjectProxy.
func ProxyObject(ctx *change.Context, root *json.Object) *ObjectProxy {
	return &ObjectProxy{
		Object:  root,
		context: ctx,
	}
}

func NewObjectProxy(
	ctx *change.Context,
	members *json.RHT,
	createdAt *time.Ticket,
) *ObjectProxy {
	return &ObjectProxy{
		Object:  json.NewObject(members, createdAt),
		context: ctx,
	}
}

func (p *ObjectProxy) SetNewObject(k string) *ObjectProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewObjectProxy(p.context, json.NewRHT(), ticket)
	})

	return v.(*ObjectProxy)
}

func (p *ObjectProxy) SetNewArray(k string) *ArrayProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewRGA(), ticket)
	})

	return v.(*ArrayProxy)
}

func (p *ObjectProxy) SetNewText(k string) *TextProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewTextProxy(p.context, json.NewRGATreeSplit(), ticket)
	})

	return v.(*TextProxy)
}

func (p *ObjectProxy) SetBool(k string, v bool) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetInteger(k string, v int) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetLong(k string, v int64) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetDouble(k string, v float64) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetString(k, v string) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetBytes(k string, v []byte) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) SetDate(k string, v time2.Time) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

func (p *ObjectProxy) Remove(k string) json.Element {
	removed := p.Object.Remove(k)

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

func (p *ObjectProxy) GetObject(k string) *ObjectProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.Object:
		return ProxyObject(p.context, elem)
	case *ObjectProxy:
		return elem
	default:
		panic("unsupported type")
	}
}

func (p *ObjectProxy) GetArray(k string) *ArrayProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.Array:
		return ProxyArray(p.context, elem)
	case *ArrayProxy:
		return elem
	default:
		panic("unsupported type")
	}
}

func (p *ObjectProxy) GetText(k string) *TextProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.Text:
		return ProxyText(p.context, elem)
	case *TextProxy:
		return elem
	default:
		panic("unsupported type")
	}
}

func (p *ObjectProxy) setInternal(
	k string,
	creator func(ticket *time.Ticket) json.Element,
) json.Element {
	ticket := p.context.IssueTimeTicket()
	value := creator(ticket)
	p.Set(k, value)

	p.context.Push(operation.NewSet(
		p.CreatedAt(),
		k,
		toOriginal(value),
		ticket,
	))

	return value
}
