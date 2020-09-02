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
	"encoding/binary"
	"fmt"
	"math"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type CounterType int

const (
	IntegerCnt CounterType = iota
	LongCnt
	DoubleCnt
)

// CounterValueFromBytes parses the given bytes into value.
func CounterValueFromBytes(counterType CounterType, value []byte) interface{} {
	switch counterType {
	case IntegerCnt:
		val := int32(binary.LittleEndian.Uint32(value))
		return int(val)
	case LongCnt:
		return int64(binary.LittleEndian.Uint64(value))
	case DoubleCnt:
		return math.Float64frombits(binary.LittleEndian.Uint64(value))
	}

	panic("unsupported type")
}

// Counter represents changeable number data type.
type Counter struct {
	valueType CounterType
	value     interface{}
	createdAt *time.Ticket
	updatedAt *time.Ticket
	removedAt *time.Ticket
}

// NewCounter creates a new instance of Counter.
func NewCounter(value interface{}, createdAt *time.Ticket) *Counter {
	switch val := value.(type) {
	case int:
		if math.MaxInt32 < val || math.MinInt32 > val {
			return &Counter{
				valueType: LongCnt,
				value:     int64(val),
				createdAt: createdAt,
			}
		}
		return &Counter{
			valueType: IntegerCnt,
			value:     val,
			createdAt: createdAt,
		}
	case int64:
		return &Counter{
			valueType: LongCnt,
			value:     val,
			createdAt: createdAt,
		}
	case float64:
		return &Counter{
			valueType: DoubleCnt,
			value:     val,
			createdAt: createdAt,
		}
	}

	panic("unsupported type")
}

// Bytes creates an array representing the value.
func (p *Counter) Bytes() []byte {
	switch val := p.value.(type) {
	case int:
		bytes := [4]byte{}
		binary.LittleEndian.PutUint32(bytes[:], uint32(val))
		return bytes[:]
	case int64:
		bytes := [8]byte{}
		binary.LittleEndian.PutUint64(bytes[:], uint64(val))
		return bytes[:]
	case float64:
		bytes := [8]byte{}
		binary.LittleEndian.PutUint64(bytes[:], math.Float64bits(val))
		return bytes[:]
	}

	panic("unsupported type")
}

// Marshal returns the JSON encoding of the value.
func (p *Counter) Marshal() string {
	switch p.valueType {
	case IntegerCnt:
		return fmt.Sprintf("%d", p.value)
	case LongCnt:
		return fmt.Sprintf("%d", p.value)
	case DoubleCnt:
		return fmt.Sprintf("%f", p.value)
	}

	panic("unsupported type")
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

// UpdatedAt returns the update time of this element.
func (p *Counter) UpdatedAt() *time.Ticket {
	return p.updatedAt
}

// SetUpdatedAt sets the update time of this element.
func (p *Counter) SetUpdatedAt(updatedAt *time.Ticket) {
	p.updatedAt = updatedAt
}

// RemovedAt returns the removal time of this element.
func (p *Counter) RemovedAt() *time.Ticket {
	return p.removedAt
}

// Remove removes this element.
func (p *Counter) Remove(removedAt *time.Ticket) bool {
	if p.removedAt == nil || removedAt.After(p.removedAt) {
		p.removedAt = removedAt
		return true
	}
	return false
}

// ValueType returns the type of the value.
func (p *Counter) ValueType() CounterType {
	return p.valueType
}

// Increase increase integer, long or double.
func (p *Counter) Increase(v *Primitive) *Counter {
	if !p.IsNumericType() || !v.IsNumericType() {
		panic("unsupported type")
	}
	switch p.valueType {
	case IntegerCnt:
		switch v.valueType {
		case Long:
			p.value = p.value.(int) + int(v.value.(int64))
		case Double:
			p.value = p.value.(int) + int(v.value.(float64))
		default:
			p.value = p.value.(int) + v.value.(int)
		}

		if p.value.(int) > math.MaxInt32 || p.value.(int) < math.MinInt32 {
			p.value = int64(p.value.(int))
			p.valueType = LongCnt
		}
	case LongCnt:
		switch v.valueType {
		case Integer:
			p.value = p.value.(int64) + int64(v.value.(int))
		case Double:
			p.value = p.value.(int64) + int64(v.value.(float64))
		default:
			p.value = p.value.(int64) + v.value.(int64)
		}
	case DoubleCnt:
		switch v.valueType {
		case Integer:
			p.value = p.value.(float64) + float64(v.value.(int))
		case Long:
			p.value = p.value.(float64) + float64(v.value.(int64))
		default:
			p.value = p.value.(float64) + v.value.(float64)
		}
	}

	return p
}

// IsNumericType checks for numeric types.
func (p *Counter) IsNumericType() bool {
	t := p.valueType
	return t == IntegerCnt || t == LongCnt || t == DoubleCnt
}
