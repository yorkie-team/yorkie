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

package json

import (
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Object represents an object in the document. As a proxy for the CRDT object,
// it is used when the user manipulates the object from the outside.
type Object struct {
	*crdt.Object
	context *change.Context
}

// NewObject creates a new instance of Object.
func NewObject(ctx *change.Context, root *crdt.Object) *Object {
	return &Object{
		Object:  root,
		context: ctx,
	}
}

// SetNewObject sets a new Object for the given key.
func (p *Object) SetNewObject(k string) (*Object, error) {
	v, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		return NewObject(p.context, crdt.NewObject(crdt.NewElementRHT(), ticket)), nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set new object: %w", err)
	}

	return v.(*Object), nil
}

// SetNewArray sets a new Array for the given key.
func (p *Object) SetNewArray(k string) (*Array, error) {
	v, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		elements, err := crdt.NewRGATreeList()
		if err != nil {
			return nil, err
		}
		return NewArray(p.context, crdt.NewArray(elements, ticket)), nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set new array: %w", err)
	}

	return v.(*Array), nil
}

// SetNewText sets a new Text for the given key.
func (p *Object) SetNewText(k string) (*Text, error) {
	v, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		rgaTreeSplit, err := crdt.NewRGATreeSplit(crdt.InitialTextNode())
		if err != nil {
			return nil, err
		}
		return NewText(
			p.context,
			crdt.NewText(rgaTreeSplit, ticket),
		), nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set new text: %w", err)
	}

	return v.(*Text), nil
}

// SetNewCounter sets a new NewCounter for the given key.
func (p *Object) SetNewCounter(k string, t crdt.CounterType, n interface{}) (*Counter, error) {
	v, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		switch t {
		case crdt.IntegerCnt:
			counter, err := crdt.NewCounter(crdt.IntegerCnt, n, ticket)
			if err != nil {
				return nil, err
			}
			newCounter, err := NewCounter(p.context, counter)
			if err != nil {
				return nil, err
			}
			return newCounter, nil
		case crdt.LongCnt:
			counter, err := crdt.NewCounter(crdt.LongCnt, n, ticket)
			if err != nil {
				return nil, err
			}
			newCounter, err := NewCounter(p.context, counter)
			if err != nil {
				return nil, err
			}
			return newCounter, nil
		default:
			return nil, fmt.Errorf("json object set new counter: unsupported type")
		}
	})
	if err != nil {
		return nil, fmt.Errorf("json object set new counter: %w", err)
	}

	return v.(*Counter), nil
}

// SetNull sets the null for the given key.
func (p *Object) SetNull(k string) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(nil, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set null: %w", err)
	}

	return p, nil
}

// SetBool sets the given boolean for the given key.
func (p *Object) SetBool(k string, v bool) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set bool: %w", err)
	}

	return p, nil
}

// SetInteger sets the given integer for the given key.
func (p *Object) SetInteger(k string, v int) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set integer: %w", err)
	}

	return p, nil
}

// SetLong sets the given long for the given key.
func (p *Object) SetLong(k string, v int64) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set long: %w", err)
	}

	return p, nil
}

// SetDouble sets the given double for the given key.
func (p *Object) SetDouble(k string, v float64) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set double: %w", err)
	}

	return p, nil
}

// SetString sets the given string for the given key.
func (p *Object) SetString(k, v string) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set string: %w", err)
	}

	return p, nil
}

// SetBytes sets the given bytes for the given key.
func (p *Object) SetBytes(k string, v []byte) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set bytes: %w", err)
	}

	return p, nil
}

// SetDate sets the given date for the given key.
func (p *Object) SetDate(k string, v gotime.Time) (*Object, error) {
	_, err := p.setInternal(k, func(ticket *time.Ticket) (crdt.Element, error) {
		primitive, err := crdt.NewPrimitive(v, ticket)
		if err != nil {
			return nil, err
		}
		return primitive, nil
	})
	if err != nil {
		return nil, fmt.Errorf("json object set date: %w", err)
	}

	return p, nil
}

// Delete deletes the value of the given key.
func (p *Object) Delete(k string) (crdt.Element, error) {
	if !p.Object.Has(k) {
		return nil, nil
	}

	ticket := p.context.IssueTimeTicket()
	deleted := p.Object.Delete(k, ticket)
	p.context.Push(operations.NewRemove(
		p.CreatedAt(),
		deleted.CreatedAt(),
		ticket,
	))
	p.context.RegisterRemovedElementPair(p, deleted)
	return deleted, nil
}

// GetObject returns Object of the given key.
func (p *Object) GetObject(k string) (*Object, error) {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil, nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Object:
		return NewObject(p.context, elem), nil
	case *Object:
		return elem, nil
	default:
		return nil, fmt.Errorf("json object get object: unsupported type")
	}
}

// GetArray returns Array of the given key.
func (p *Object) GetArray(k string) (*Array, error) {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil, nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Array:
		return NewArray(p.context, elem), nil
	case *Array:
		return elem, nil
	default:
		return nil, fmt.Errorf("json object get array: unsupported type")
	}
}

// GetText returns Text of the given key.
func (p *Object) GetText(k string) (*Text, error) {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil, nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Text:
		return NewText(p.context, elem), nil
	case *Text:
		return elem, nil
	default:
		return nil, fmt.Errorf("json object get text: unsupported type")
	}
}

// GetCounter returns Counter of the given key.
func (p *Object) GetCounter(k string) (*Counter, error) {
	elem := p.Object.Get(k)
	if elem == nil {
		return nil, nil
	}

	switch elem := p.Object.Get(k).(type) {
	case *crdt.Counter:
		newCounter, err := NewCounter(p.context, elem)
		if err != nil {
			return nil, fmt.Errorf("json object get counter: %w", err)
		}
		return newCounter, nil
	case *Counter:
		return elem, nil
	default:
		return nil, fmt.Errorf("json object get counter: unsupported type")
	}
}

func (p *Object) setInternal(
	k string,
	creator func(ticket *time.Ticket) (crdt.Element, error),
) (crdt.Element, error) {
	ticket := p.context.IssueTimeTicket()
	elem, err := creator(ticket)
	if err != nil {
		return nil, fmt.Errorf("json object set internal: %w", err)
	}
	value, err := toOriginal(elem)
	if err != nil {
		return nil, fmt.Errorf("json object set internal: %w", err)
	}

	copiedValue, err := value.DeepCopy()
	if err != nil {
		return nil, fmt.Errorf("json object set internal: %w", err)
	}
	p.context.Push(operations.NewSet(
		p.CreatedAt(),
		k,
		copiedValue,
		ticket,
	))

	removed := p.Set(k, value)
	p.context.RegisterElement(value)
	if removed != nil {
		p.context.RegisterRemovedElementPair(p, removed)
	}

	return elem, nil
}
