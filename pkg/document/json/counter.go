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
	"reflect"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
)

// Counter represents a counter in the document. As a proxy for the CRDT counter,
// it is used when the user manipulates the counter from the outside.
type Counter struct {
	*crdt.Counter
	context   *change.Context
	valueType crdt.CounterType
	value     interface{}
}

// NewCounter creates a new instance of Counter.
func NewCounter(n interface{}, t crdt.CounterType) *Counter {
	return &Counter{
		valueType: t,
		value:     n,
	}
}

// Initialize initializes the Counter by the given context and counter.
func (p *Counter) Initialize(ctx *change.Context, counter *crdt.Counter) *Counter {
	if !counter.IsNumericType() {
		panic("unsupported type")
	}
	p.Counter = counter
	p.context = ctx

	return p
}

// Increase adds an increase operations.
// Only numeric types are allowed as operand values, excluding
// uint64 and uintptr.
func (p *Counter) Increase(v interface{}) *Counter {
	if !isAllowedOperand(v) {
		panic("unsupported type")
	}
	var primitive *crdt.Primitive
	var err error
	ticket := p.context.IssueTimeTicket()

	value, kind := convertAssertableOperand(v)
	isInt := kind == reflect.Int
	switch p.ValueType() {
	case crdt.LongCnt:
		if isInt {
			primitive, err = crdt.NewPrimitive(int64(value.(int)), ticket)
		} else {
			primitive, err = crdt.NewPrimitive(int64(value.(float64)), ticket)
		}
	case crdt.IntegerCnt:
		if isInt {
			primitive, err = crdt.NewPrimitive(int32(value.(int)), ticket)
		} else {
			primitive, err = crdt.NewPrimitive(int32(value.(float64)), ticket)
		}
	default:
		panic("unsupported type")
	}
	if err != nil {
		panic(err)
	}
	if _, err := p.Counter.Increase(primitive); err != nil {
		panic(err)
	}

	p.context.Push(operations.NewIncrease(
		p.CreatedAt(),
		primitive,
		ticket,
	))

	return p
}

// isAllowedOperand indicates whether
// the operand of increase is an allowable type.
func isAllowedOperand(v interface{}) bool {
	vt := reflect.ValueOf(v).Kind()
	if vt >= reflect.Int && vt <= reflect.Float64 && vt != reflect.Uint64 && vt != reflect.Uintptr {
		return true
	}

	return false
}

// convertAssertableOperand converts the operand
// to be used in the increase function to assertable type.
func convertAssertableOperand(v interface{}) (interface{}, reflect.Kind) {
	vt := reflect.ValueOf(v).Kind()
	switch vt {
	case reflect.Int:
		return v, reflect.Int
	case reflect.Int8:
		return int(v.(int8)), reflect.Int
	case reflect.Int16:
		return int(v.(int16)), reflect.Int
	case reflect.Int32:
		return int(v.(int32)), reflect.Int
	case reflect.Int64:
		return int(v.(int64)), reflect.Int
	case reflect.Uint:
		return int(v.(uint)), reflect.Int
	case reflect.Uint8:
		return int(v.(uint8)), reflect.Int
	case reflect.Uint16:
		return int(v.(uint16)), reflect.Int
	case reflect.Uint32:
		return int(v.(uint32)), reflect.Int
	case reflect.Float32:
		return float64(v.(float32)), reflect.Float64
	default:
		return v, reflect.Float64
	}
}
