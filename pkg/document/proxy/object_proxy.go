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
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ObjectProxy is a proxy representing Object.
type ObjectProxy struct {
	*json.Object
	context *change.Context
}

// NewObjectProxy creates a new instance of ObjectProxy.
func NewObjectProxy(ctx *change.Context, root *json.Object) *ObjectProxy {
	return &ObjectProxy{
		Object:  root,
		context: ctx,
	}
}

// SetNewObject sets a new Object for the given key.
func (p *ObjectProxy) SetNewObject(k string) *ObjectProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewObjectProxy(p.context, json.NewObject(json.NewRHTPriorityQueueMap(), ticket))
	})

	return v.(*ObjectProxy)
}

// SetNewArray sets a new Array for the given key.
func (p *ObjectProxy) SetNewArray(k string) *ArrayProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewArrayProxy(p.context, json.NewArray(json.NewRGATreeList(), ticket))
	})

	return v.(*ArrayProxy)
}

// SetNewText sets a new Text for the given key.
func (p *ObjectProxy) SetNewText(k string) *TextProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewTextProxy(
			p.context,
			json.NewText(json.NewRGATreeSplit(json.InitialTextNode()), ticket),
		)
	})

	return v.(*TextProxy)
}

// SetNewRichText sets a new RichText for the given key.
func (p *ObjectProxy) SetNewRichText(k string) *RichTextProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewRichTextProxy(
			p.context,
			json.NewInitialRichText(json.NewRGATreeSplit(json.InitialRichTextNode()), ticket),
		)
	})

	return v.(*RichTextProxy)
}

// SetNewCounter sets a new NewCounter for the given key.
func (p *ObjectProxy) SetNewCounter(k string, n interface{}) *CounterProxy {
	v := p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return NewCounterProxy(
			p.context,
			json.NewCounter(n, ticket),
		)
	})

	return v.(*CounterProxy)
}

// SetBool sets the given boolean for the given key.
func (p *ObjectProxy) SetBool(k string, v bool) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetInteger sets the given integer for the given key.
func (p *ObjectProxy) SetInteger(k string, v int) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetLong sets the given long for the given key.
func (p *ObjectProxy) SetLong(k string, v int64) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetDouble sets the given double for the given key.
func (p *ObjectProxy) SetDouble(k string, v float64) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetString sets the given string for the given key.
func (p *ObjectProxy) SetString(k, v string) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetBytes sets the given bytes for the given key.
func (p *ObjectProxy) SetBytes(k string, v []byte) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// SetDate sets the given date for the given key.
func (p *ObjectProxy) SetDate(k string, v gotime.Time) *ObjectProxy {
	p.setInternal(k, func(ticket *time.Ticket) json.Element {
		return json.NewPrimitive(v, ticket)
	})

	return p
}

// Delete deletes the value of the given key.
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
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted
}

// GetObject returns Object of the given key.
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

// GetArray returns Array of the given key.
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

// GetText returns Text of the given key.
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

// GetRichText returns RichText of the given key.
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

// GetCounter returns CounterProxy of the given key.
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
