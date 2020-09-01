/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package proxy

import (
	time2 "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
		return NewObjectProxy(p.context, json.NewObject(json.NewRHTPriorityQueueMap(), ticket))
	})

	return v.(*ObjectProxy)
}

func (p *ObjectProxy) SetNewArray(k string) *ArrayProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGATreeList(), ticket))
	})

	return v.(*ArrayProxy)
}

func (p *ObjectProxy) SetNewText(k string) *TextProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewTextProxy(
			p.context,
			json.NewText(json.NewRGATreeSplit(json.InitialTextNode()), ticket),
		)
	})

	return v.(*TextProxy)
}

func (p *ObjectProxy) SetNewRichText(k string) *RichTextProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewRichTextProxy(
			p.context,
			json.NewInitialRichText(json.NewRGATreeSplit(json.InitialRichTextNode()), ticket),
		)
	})

	return v.(*RichTextProxy)
}

func (p *ObjectProxy) SetNewCounter(k string, n interface{}) *CounterProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewCounterProxy(
			p.context,
			json.NewCounter(n, ticket),
		)
	})

	return v.(*CounterProxy)
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

func (p *ObjectProxy) Delete(k string) json.Element {
	if !p.Object.Has(k) {
		return nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted := p.Object.Delete(k, ticket)
	p.context.Push(operation.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))
	return deleted
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
		return NewTextProxy(p.context, elem)
	case *TextProxy:
		return elem
	default:
		panic("unsupported type")
	}
}

func (p *ObjectProxy) GetRichText(k string) *RichTextProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.RichText:
		return NewRichTextProxy(p.context, elem)
	case *RichTextProxy:
		return elem
	default:
		panic("unsupported type")
	}
}

// GetCounter gets a CounterProxy.
func (p *ObjectProxy) GetCounter(k string) *CounterProxy {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *json.Counter:
		return NewCounterProxy(p.context, elem)
	case *CounterProxy:
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
		value.DeepCopy(),
		ticket,
	))

	p.Set(k, value)
	p.context.RegisterElement(value)

	return proxy
}
