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

package crdt

import (
	"encoding/binary"
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// CounterType represents any type that can be used as a counter.
type CounterType int

// The values below are the types that can be used as counters.
const (
	IntegerCnt CounterType = iota
	LongCnt
)

// CounterValueFromBytes parses the given bytes into value.
func CounterValueFromBytes(counterType CounterType, value []byte) interface{} {
	switch counterType {
	case IntegerCnt:
		val := int32(binary.LittleEndian.Uint32(value))
		return int(val)
	case LongCnt:
		return int64(binary.LittleEndian.Uint64(value))
	}

	panic("unsupported type")
}

// Counter represents changeable number data type.
type Counter struct {
	valueType CounterType
	value     interface{}
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewCounter creates a new instance of Counter.
func NewCounter(valueType CounterType, value interface{}, createdAt *time.Ticket) *Counter {
	switch valueType {
	case IntegerCnt:
		switch val := value.(type) {
		case int32:
			return &Counter{
				valueType: IntegerCnt,
				value:     val,
				createdAt: createdAt,
			}
		case int64:
			return &Counter{
				valueType: IntegerCnt,
				value:     int32(val),
				createdAt: createdAt,
			}
		case int:
			return &Counter{
				valueType: IntegerCnt,
				value:     int32(val),
				createdAt: createdAt,
			}
		case float32:
			return &Counter{
				valueType: IntegerCnt,
				value:     int32(val),
				createdAt: createdAt,
			}
		case float64:
			return &Counter{
				valueType: IntegerCnt,
				value:     int32(val),
				createdAt: createdAt,
			}
		default:
			panic("unsupported type")
		}
	case LongCnt:
		switch val := value.(type) {
		case int64:
			return &Counter{
				valueType: LongCnt,
				value:     val,
				createdAt: createdAt,
			}
		case int32:
			return &Counter{
				valueType: LongCnt,
				value:     int64(val),
				createdAt: createdAt,
			}
		case int:
			return &Counter{
				valueType: LongCnt,
				value:     int64(val),
				createdAt: createdAt,
			}
		case float32:
			return &Counter{
				valueType: LongCnt,
				value:     int64(val),
				createdAt: createdAt,
			}
		case float64:
			return &Counter{
				valueType: LongCnt,
				value:     int64(val),
				createdAt: createdAt,
			}
		default:
			panic("unsupported type")
		}
	}

	panic("unsupported type")
}

// Bytes creates an array representing the value.
func (p *Counter) Bytes() []byte {
	switch val := p.value.(type) {
	case int32:
		bytes := [4]byte{}
		binary.LittleEndian.PutUint32(bytes[:], uint32(val))
		return bytes[:]
	case int64:
		bytes := [8]byte{}
		binary.LittleEndian.PutUint64(bytes[:], uint64(val))
		return bytes[:]
	}

	panic("unsupported type")
}

// Marshal returns the JSON encoding of the value.
func (p *Counter) Marshal() string {
	return fmt.Sprintf("%d", p.value)
}

// DeepCopy copies itself deeply.
func (p *Counter) DeepCopy() Element {
	counter := *p
	return &counter
}

// CreatedAt returns the creation time.
func (p *Counter) CreatedAt() *time.Ticket {
	return p.createdAt
}

// MovedAt returns the move time of this element.
func (p *Counter) MovedAt() *time.Ticket {
	return p.movedAt
}

// SetMovedAt sets the move time of this element.
func (p *Counter) SetMovedAt(movedAt *time.Ticket) {
	p.movedAt = movedAt
}

// RemovedAt returns the removal time of this element.
func (p *Counter) RemovedAt() *time.Ticket {
	return p.removedAt
}

// SetRemovedAt sets the removal time of this element.
func (p *Counter) SetRemovedAt(removedAt *time.Ticket) {
	p.removedAt = removedAt
}

// Remove removes this element.
func (p *Counter) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(p.createdAt)) &&
		(p.removedAt == nil || removedAt.After(p.removedAt)) {
		p.removedAt = removedAt
		return true
	}
	return false
}

// ValueType returns the type of the value.
func (p *Counter) ValueType() CounterType {
	return p.valueType
}

// Increase increases integer, long or double.
// If the result of the operation is greater than MaxInt32 or less
// than MinInt32, Counter's value type can be changed Integer to Long.
// Because in golang, int can be either int32 or int64.
// So we need to assert int to int32.
func (p *Counter) Increase(v *Primitive) *Counter {
	if !p.IsNumericType() || !v.IsNumericType() {
		panic("unsupported type")
	}
	switch p.valueType {
	case IntegerCnt:
		switch v.valueType {
		case Long:
			p.value = p.value.(int32) + int32(v.value.(int64))
		case Double:
			p.value = p.value.(int32) + int32(v.value.(float64))
		default:
			p.value = p.value.(int32) + v.value.(int32)
		}
	case LongCnt:
		switch v.valueType {
		case Integer:
			p.value = p.value.(int64) + int64(v.value.(int32))
		case Double:
			p.value = p.value.(int64) + int64(v.value.(float64))
		default:
			p.value = p.value.(int64) + v.value.(int64)
		}
	}

	return p
}

// IsNumericType checks for numeric types.
func (p *Counter) IsNumericType() bool {
	t := p.valueType
	return t == IntegerCnt || t == LongCnt
}
