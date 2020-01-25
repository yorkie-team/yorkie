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

func NewObjectProxy(ctx *change.Context, root *json.Object) *ObjectProxy {
	return &ObjectProxy{
		Object:  root,
		context: ctx,
	}
}

func (p *ObjectProxy) SetNewObject(k string) *ObjectProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewObjectProxy(p.context, json.NewObject(json.NewRHT(), ticket))
	})

	return v.(*ObjectProxy)
}

func (p *ObjectProxy) SetNewArray(k string) *ArrayProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGA(), ticket))
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
	if !p.Object.Has(k) {
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	removed := p.Object.Remove(k, ticket)
	p.context.Push(operation.NewRemove(
		p.CreatedAt(),
		removed.CreatedAt(),
		ticket,
	))
	return removed
}

func (p *ObjectProxy) GetObject(k string) *ObjectProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.Object:
		return NewObjectProxy(p.context, elem)
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
		return NewArrayProxy(p.context, elem)
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
	proxy := creator(ticket)
	value := toOriginal(proxy)

	p.context.Push(operation.NewSet(
		p.CreatedAt(),
		k,
		value.Deepcopy(),
		ticket,
	))

	p.Set(k, value)
	p.context.RegisterElement(value)

	return proxy
}
