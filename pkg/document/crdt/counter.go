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
	"errors"
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ErrUnsupportedType is returned when the given type is not supported.
var ErrUnsupportedType = errors.New("unsupported type")

// ErrDedupRequiresActor is returned when a dedup counter receives an
// increase without an actor.
var ErrDedupRequiresActor = errors.New("dedup counter requires actor")

// ErrDedupIncrementMustBeOne is returned when a dedup counter receives
// an increment value other than 1.
var ErrDedupIncrementMustBeOne = errors.New("dedup counter only supports increment by 1")

// CounterType represents any type that can be used as a counter.
type CounterType int

// The values below are the types that can be used as counters.
const (
	IntegerCnt CounterType = iota
	LongCnt
	IntegerDedupCnt
)

// CounterValueFromBytes parses the given bytes into value.
func CounterValueFromBytes(counterType CounterType, value []byte) (interface{}, error) {
	switch counterType {
	case IntegerCnt, IntegerDedupCnt:
		val := int32(binary.LittleEndian.Uint32(value))
		return int(val), nil
	case LongCnt:
		return int64(binary.LittleEndian.Uint64(value)), nil
	default:
		return nil, ErrUnsupportedType
	}
}

// Counter represents changeable number data type.
type Counter struct {
	valueType CounterType
	value     interface{}
	createdAt *time.Ticket
	movedAt   *time.Ticket
	removedAt *time.Ticket
	hll       *HLL
}

// NewCounter creates a new instance of Counter.
func NewCounter(valueType CounterType, value interface{}, createdAt *time.Ticket) (*Counter, error) {
	switch valueType {
	case IntegerCnt:
		intValue, err := castToInt(value)
		if err != nil {
			return nil, err
		}
		return &Counter{
			valueType: valueType,
			value:     intValue,
			createdAt: createdAt,
		}, nil
	case IntegerDedupCnt:
		return &Counter{
			valueType: valueType,
			value:     int32(0),
			createdAt: createdAt,
			hll:       NewHLL(),
		}, nil
	case LongCnt:
		longValue, err := castToLong(value)
		if err != nil {
			return nil, err
		}
		return &Counter{
			valueType: valueType,
			value:     longValue,
			createdAt: createdAt,
		}, nil
	default:
		return nil, ErrUnsupportedType
	}
}

// Bytes creates an array representing the value.
func (p *Counter) Bytes() ([]byte, error) {
	switch val := p.value.(type) {
	case int32:
		bytes := [4]byte{}
		binary.LittleEndian.PutUint32(bytes[:], uint32(val))
		return bytes[:], nil
	case int64:
		bytes := [8]byte{}
		binary.LittleEndian.PutUint64(bytes[:], uint64(val))
		return bytes[:], nil
	default:
		return nil, ErrUnsupportedType
	}
}

// MetaSize returns the size of the metadata of this element.
func (p *Counter) MetaSize() int {
	size := 0
	if p.createdAt != nil {
		size += time.TicketSize
	}
	if p.movedAt != nil {
		size += time.TicketSize
	}
	if p.removedAt != nil {
		size += time.TicketSize
	}
	return size
}

// DataSize returns the data usage of this element.
func (p *Counter) DataSize() resource.DataSize {
	size := 0
	if p.valueType == IntegerCnt || p.valueType == IntegerDedupCnt {
		size = 4
	} else if p.valueType == LongCnt {
		size = 8
	}
	if p.IsDedup() && p.hll != nil {
		size += len(p.hll.Bytes())
	}
	return resource.DataSize{
		Data: size,
		Meta: p.MetaSize(),
	}
}

// Marshal returns the JSON encoding of the value.
func (p *Counter) Marshal() string {
	return fmt.Sprintf("%d", p.value)
}

// DeepCopy copies itself deeply.
func (p *Counter) DeepCopy() (Element, error) {
	counter := *p
	if p.IsDedup() && p.hll != nil {
		counter.hll = NewHLL()
		if err := counter.hll.Restore(p.hll.Bytes()); err != nil {
			return nil, err
		}
	}
	return &counter, nil
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

// Value returns the value of this counter.
// TODO(hackerwins): We need to use generics to avoid using interface{}.
func (p *Counter) Value() interface{} {
	return p.value
}

// Increase increases integer, long or double.
// If the result of the operation is greater than MaxInt32 or less
// than MinInt32, Counter's value type can be changed Integer to Long.
// Because in golang, int can be either int32 or int64.
// So we need to assert int to int32.
// Dedup counters must use IncreaseDedup instead.
func (p *Counter) Increase(v *Primitive) (*Counter, error) {
	if p.IsDedup() {
		return nil, ErrDedupRequiresActor
	}
	if !p.IsNumericType() || !v.IsNumericType() {
		return nil, ErrUnsupportedType
	}
	switch p.valueType {
	case IntegerCnt:
		intValue, err := castToInt(v.value)
		if err != nil {
			return nil, err
		}
		p.value = p.value.(int32) + intValue
	case LongCnt:
		longValue, err := castToLong(v.value)
		if err != nil {
			return nil, err
		}
		p.value = p.value.(int64) + longValue
	default:
		return nil, ErrUnsupportedType
	}

	return p, nil
}

// IsNumericType checks for numeric types.
func (p *Counter) IsNumericType() bool {
	t := p.valueType
	return t == IntegerCnt || t == LongCnt || t == IntegerDedupCnt
}

// IsDedup returns whether this Counter is in dedup mode.
func (p *Counter) IsDedup() bool {
	return p.valueType == IntegerDedupCnt
}

// IncreaseDedup increases the counter only if the actor has not been seen
// before. The delta value must be 1. Returns the counter and nil error on success.
func (p *Counter) IncreaseDedup(v *Primitive, actor string) (*Counter, error) {
	if !p.IsDedup() || p.hll == nil {
		return p.Increase(v)
	}

	if !p.IsNumericType() || !v.IsNumericType() {
		return nil, ErrUnsupportedType
	}

	if actor == "" {
		return nil, ErrDedupRequiresActor
	}

	switch val := v.value.(type) {
	case int32:
		if val != 1 {
			return nil, ErrDedupIncrementMustBeOne
		}
	case int64:
		if val != 1 {
			return nil, ErrDedupIncrementMustBeOne
		}
	case int:
		if val != 1 {
			return nil, ErrDedupIncrementMustBeOne
		}
	default:
		// float types are not allowed for dedup increment.
		return nil, ErrDedupIncrementMustBeOne
	}

	if p.hll.Add(actor) {
		p.recomputeValue()
	}

	return p, nil
}

// HLLBytes returns the serialized HLL registers. Returns nil if not in dedup mode.
func (p *Counter) HLLBytes() []byte {
	if p.hll == nil {
		return nil
	}
	return p.hll.Bytes()
}

// RestoreHLL restores the HLL state from bytes and recomputes the counter value.
func (p *Counter) RestoreHLL(data []byte) error {
	if p.hll == nil {
		p.hll = NewHLL()
	}
	if err := p.hll.Restore(data); err != nil {
		return err
	}
	p.recomputeValue()
	return nil
}

// recomputeValue sets the counter value from the HLL count.
func (p *Counter) recomputeValue() {
	count := p.hll.Count()
	switch p.valueType {
	case IntegerDedupCnt:
		p.value = int32(count)
	}
}

// castToInt casts numeric type to int32.
func castToInt(value interface{}) (int32, error) {
	switch val := value.(type) {
	case int32:
		return val, nil
	case int64:
		return int32(val), nil
	case int:
		return int32(val), nil
	case float32:
		return int32(val), nil
	case float64:
		return int32(val), nil
	default:
		return 0, ErrUnsupportedType
	}
}

// castToLong casts numeric type to int64.
func castToLong(value interface{}) (int64, error) {
	switch val := value.(type) {
	case int64:
		return val, nil
	case int32:
		return int64(val), nil
	case int:
		return int64(val), nil
	case float32:
		return int64(val), nil
	case float64:
		return int64(val), nil
	default:
		return 0, ErrUnsupportedType
	}
}
