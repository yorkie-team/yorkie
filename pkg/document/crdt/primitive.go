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
	"math"
	gotime "time"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ValueType represents the type of Primitive value.
type ValueType int

// Primitive can have the following types:
const (
	Null ValueType = iota
	Boolean
	Integer
	Long
	Double
	String
	Bytes
	Date
)

// ValueFromBytes parses the given bytes into value.
func ValueFromBytes(valueType ValueType, value []byte) interface{} {
	switch valueType {
	case Null:
		return nil
	case Boolean:
		if value[0] == 1 {
			return true
		}
		return false
	case Integer:
		val := int32(binary.LittleEndian.Uint32(value))
		return val
	case Long:
		return int64(binary.LittleEndian.Uint64(value))
	case Double:
		return math.Float64frombits(binary.LittleEndian.Uint64(value))
	case String:
		return string(value)
	case Bytes:
		return value
	case Date:
		v := int64(binary.LittleEndian.Uint64(value))
		return gotime.UnixMilli(v)
	}

	panic("unsupported type")
}

// Primitive represents JSON primitive data type including logical lock.
type Primitive struct {
	valueType ValueType
	value     interface{}
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
}

// NewPrimitive creates a new instance of Primitive.
func NewPrimitive(value interface{}, createdAt *time.Ticket) *Primitive {
	if value == nil {
		return &Primitive{
			valueType: Null,
			value:     nil,
			createdAt: createdAt,
		}
	}

	switch val := value.(type) {
	case bool:
		return &Primitive{
			valueType: Boolean,
			value:     val,
			createdAt: createdAt,
		}
	case int32:
		return &Primitive{
			valueType: Integer,
			value:     val,
			createdAt: createdAt,
		}
	case int64:
		return &Primitive{
			valueType: Long,
			value:     val,
			createdAt: createdAt,
		}
	case int:
		if val > math.MaxInt32 || val < math.MinInt32 {
			return &Primitive{
				valueType: Long,
				value:     int64(val),
				createdAt: createdAt,
			}
		}
		return &Primitive{
			valueType: Integer,
			value:     int32(val),
			createdAt: createdAt,
		}
	case float32:
		return &Primitive{
			valueType: Double,
			value:     float64(val),
			createdAt: createdAt,
		}
	case float64:
		return &Primitive{
			valueType: Double,
			value:     val,
			createdAt: createdAt,
		}
	case string:
		return &Primitive{
			valueType: String,
			value:     val,
			createdAt: createdAt,
		}
	case []byte:
		return &Primitive{
			valueType: Bytes,
			value:     val,
			createdAt: createdAt,
		}
	case gotime.Time:
		return &Primitive{
			valueType: Date,
			value:     val,
			createdAt: createdAt,
		}
	}

	panic("unsupported type")
}

// Bytes creates an array representing the value.
func (p *Primitive) Bytes() []byte {
	if p.valueType == Null {
		return nil
	}

	switch val := p.value.(type) {
	case bool:
		if val {
			return []byte{1}
		}
		return []byte{0}
	case int32:
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
	case string:
		return []byte(val)
	case []byte:
		return val
	case gotime.Time:
		bytes := [8]byte{}
		binary.LittleEndian.PutUint64(bytes[:], uint64(val.UTC().UnixMilli()))
		return bytes[:]
	}

	panic("unsupported type")
}

// Marshal returns the JSON encoding of the value.
func (p *Primitive) Marshal() string {
	switch p.valueType {
	case Null:
		return "null"
	case Boolean:
		return fmt.Sprintf("%t", p.value)
	case Integer:
		return fmt.Sprintf("%d", p.value)
	case Long:
		return fmt.Sprintf("%d", p.value)
	case Double:
		return fmt.Sprintf("%f", p.value)
	case String:
		return fmt.Sprintf(`"%s"`, EscapeString(p.value.(string)))
	case Bytes:
		// TODO: JSON.stringify({a: new Uint8Array([1,2]), b: 2})
		// {"a":{"0":1,"1":2},"b":2}
		return fmt.Sprintf(`"%s"`, p.value)
	case Date:
		return fmt.Sprintf(`"%s"`, p.value.(gotime.Time).Format(gotime.RFC3339))
	}

	panic("unsupported type")
}

// DeepCopy copies itself deeply.
func (p *Primitive) DeepCopy() (Element, error) {
	primitive := *p
	return &primitive, nil
}

// CreatedAt returns the creation time.
func (p *Primitive) CreatedAt() *time.Ticket {
	return p.createdAt
}

// MovedAt returns the move time of this element.
func (p *Primitive) MovedAt() *time.Ticket {
	return p.movedAt
}

// SetMovedAt sets the move time of this element.
func (p *Primitive) SetMovedAt(movedAt *time.Ticket) {
	p.movedAt = movedAt
}

// RemovedAt returns the removal time of this element.
func (p *Primitive) RemovedAt() *time.Ticket {
	return p.removedAt
}

// SetRemovedAt sets the removal time of this element.
func (p *Primitive) SetRemovedAt(removedAt *time.Ticket) {
	p.removedAt = removedAt
}

// Remove removes this element.
func (p *Primitive) Remove(removedAt *time.Ticket) bool {
	if (removedAt != nil && removedAt.After(p.createdAt)) &&
		(p.removedAt == nil || removedAt.After(p.removedAt)) {
		p.removedAt = removedAt
		return true
	}
	return false
}

// Value returns the value of Primitive.
func (p *Primitive) Value() interface{} {
	return p.value
}

// ValueType returns the type of the value.
func (p *Primitive) ValueType() ValueType {
	return p.valueType
}

// IsNumericType checks for numeric types.
func (p *Primitive) IsNumericType() bool {
	t := p.valueType
	return t == Integer || t == Long || t == Double
}
