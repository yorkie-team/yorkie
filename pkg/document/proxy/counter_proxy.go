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
	"reflect"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operation"
)

// CounterProxy is a proxy representing counter.
type CounterProxy struct {
	*json.Counter
	context *change.Context
}

// NewCounterProxy create CounterProxy instance.
func NewCounterProxy(ctx *change.Context, counter *json.Counter) *CounterProxy {
	if !counter.IsNumericType() {
		panic("unsupported type")
	}
	return &CounterProxy{
		Counter: counter,
		context: ctx,
	}
}

// Increase adds an increase operation.
// Only numeric types are allowed as operand values, excluding uint64 and uintptr.
func (p *CounterProxy) Increase(v interface{}) *CounterProxy {
	if !isAllowedOperand(v) {
		panic("unsupported type")
	}
	var primitive *json.Primitive
	ticket := p.context.IssueTimeTicket()

	value, kind := convertAssertableOperand(v)
	isInt := kind == reflect.Int
	switch p.ValueType() {
	case json.LongCnt:
		if isInt {
			primitive = json.NewPrimitive(int64(value.(int)), ticket)
		} else {
			primitive = json.NewPrimitive(int64(value.(float64)), ticket)
		}
	case json.IntegerCnt:
		if isInt {
			primitive = json.NewPrimitive(value, ticket)
		} else {
			primitive = json.NewPrimitive(int(value.(float64)), ticket)
		}
	case json.DoubleCnt:
		if isInt {
			primitive = json.NewPrimitive(float64(value.(int)), ticket)
		} else {
			primitive = json.NewPrimitive(value, ticket)
		}
	default:
		panic("unsupported type")
	}

	p.context.Push(operation.NewIncrease(
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
