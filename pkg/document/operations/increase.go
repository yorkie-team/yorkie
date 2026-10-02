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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Increase represents an operation that increments a numeric value to Counter.
// Among Primitives, numeric types Integer, Long, and Double are used as values.
type Increase struct {
	parentCreatedAt *time.Ticket
	value           crdt.Element
	executedAt      *time.Ticket
	actor           string
}

// NewIncrease creates the increase instance.
func NewIncrease(
	parentCreatedAt *time.Ticket,
	value crdt.Element,
	executedAt *time.Ticket,
) *Increase {
	return &Increase{
		parentCreatedAt: parentCreatedAt,
		value:           value,
		executedAt:      executedAt,
	}
}

// NewIncreaseWithActor creates a new instance of Increase with an actor for dedup mode.
func NewIncreaseWithActor(
	parentCreatedAt *time.Ticket,
	value crdt.Element,
	executedAt *time.Ticket,
	actor string,
) *Increase {
	return &Increase{
		parentCreatedAt: parentCreatedAt,
		value:           value,
		executedAt:      executedAt,
		actor:           actor,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Increase) Execute(root *crdt.Root, source OpSource, _ time.VersionVector) (ExecutionResult, error) {
	parent := root.FindByCreatedAt(o.parentCreatedAt)
	cnt, ok := parent.(*crdt.Counter)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	// Comma-ok, and a no-op rather than an error off the local path: the
	// value arrives straight off the wire (converter.fromIncrease passes
	// whatever element the pack carries), so a raw client can push an
	// Increase whose value is a Text, an Object or another Counter. The
	// server stores a pushed change before executing it (packs.PushPull), so
	// a panic here takes the RPC goroutine down and an error here makes
	// every later replay of that document fail forever. Dropping the delta
	// is deterministic on every replica, so they still converge.
	value, ok := o.value.(*crdt.Primitive)
	if !ok {
		return skipUnlessLocal(source, ErrNotApplicableDataType)
	}

	// Compute the reverse before mutating the counter, mirroring the JS SDK
	// (increase_operation.ts:95-130). A dedup counter (o.actor != "")
	// produces no reverse: HyperLogLog cannot remove an actor once added.
	// Skipped when the source discards the reverse (see OpSource.NeedsReverse):
	// a remote apply or a server replay must not fail on a reverse it throws
	// away, as every Double delta did before negatePrimitive handled it.
	//
	// The one exception is a Double delta on an Integer counter. It is added
	// in float64 and the sum is wrapped to 32 bits, so a large delta can
	// round the old value away (10 + 2^60 gives 0), and its negation cannot
	// undo that. Its reverse is the change the counter actually made, taken
	// after the apply, which int32 arithmetic always undoes.
	needsReverse := o.actor == "" && source.NeedsReverse()
	reverseFromChange := needsReverse &&
		value.ValueType() == crdt.Double && cnt.ValueType() == crdt.IntegerCnt

	var reverseOp Operation
	var before int32
	if reverseFromChange {
		before = cnt.Value().(int32)
	} else if needsReverse {
		negated, err := negatePrimitive(value)
		if err != nil {
			return ExecutionResult{}, err
		}
		reverseOp = NewIncrease(o.parentCreatedAt, negated, o.executedAt)
	}

	// Every failure below is a property of the delta the change carries -- a
	// dedup counter increased without an actor, a non-numeric primitive --
	// so it is fatal only for a change this replica is producing. For one
	// another replica or the server already stored, it is dropped for the
	// reason above: a stored change that cannot be applied is unrecoverable.
	if cnt.IsDedup() {
		if o.actor == "" {
			return skipUnlessLocal(source, ErrNotApplicableDataType)
		}
		if _, err := cnt.IncreaseDedup(value, o.actor); err != nil {
			return skipUnlessLocal(source, err)
		}
	} else {
		if _, err := cnt.Increase(value); err != nil {
			return skipUnlessLocal(source, err)
		}
	}

	if reverseFromChange {
		change, err := crdt.NewPrimitive(before-cnt.Value().(int32), value.CreatedAt())
		if err != nil {
			return ExecutionResult{}, err
		}
		reverseOp = NewIncrease(o.parentCreatedAt, change, o.executedAt)
	}

	return ExecutionResult{Reverse: reverseOp, Observable: true}, nil
}

// skipUnlessLocal returns err for an operation this replica is producing, and
// a no-op result for one that arrived from another replica or from the
// server's replay of a change it has already stored. Such a change cannot be
// rejected any more -- pushpull.PushPull persists a pushed change before it is
// ever executed -- so an error would make every later BuildInternalDocForServerSeq
// and every snapshot of that document fail forever.
func skipUnlessLocal(source OpSource, err error) (ExecutionResult, error) {
	if source == OpSourceLocal || source == OpSourceUndoRedo {
		return ExecutionResult{}, err
	}

	return ExecutionResult{Observable: false}, nil
}

// negatePrimitive returns a deep copy of the given primitive with its
// numeric value negated. It mirrors the JS SDK's toReverseOperation
// (increase_operation.ts:118-129), handling both Long (int64) and Integer
// (int32) counter deltas.
//
// NOTE(hackerwins): the JS SDK never overflows here because Long deltas are
// bigint (arbitrary precision) and Integer deltas are auto-promoted to Long
// when they exceed the int32 range. Go's Counter has no wider type to
// promote into, so negating math.MinInt32/math.MinInt64 wraps around to the
// same value, consistent with the unchecked arithmetic Counter.Increase
// already performs elsewhere in this package.
func negatePrimitive(value *crdt.Primitive) (*crdt.Primitive, error) {
	switch value.ValueType() {
	case crdt.Long:
		v, ok := value.Value().(int64)
		if !ok {
			return nil, ErrNotApplicableDataType
		}
		return crdt.NewPrimitive(-v, value.CreatedAt())
	case crdt.Integer:
		v, ok := value.Value().(int32)
		if !ok {
			return nil, ErrNotApplicableDataType
		}
		return crdt.NewPrimitive(-v, value.CreatedAt())
	case crdt.Double:
		// A JS client sends a fractional delta as a Double; JS negates it
		// as a Double too.
		v, ok := value.Value().(float64)
		if !ok {
			return nil, ErrNotApplicableDataType
		}
		return crdt.NewPrimitive(-v, value.CreatedAt())
	default:
		return nil, ErrNotApplicableDataType
	}
}

// Value return the value of this operation.
func (o *Increase) Value() crdt.Element {
	return o.value
}

// ParentCreatedAt returns the creation time of Counter.
func (o *Increase) ParentCreatedAt() *time.Ticket {
	return o.parentCreatedAt
}

// ExecutedAt returns execution time of this operation.
func (o *Increase) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// SetActor sets the given actor to this operation.
func (o *Increase) SetActor(actorID time.ActorID) {
	o.executedAt = o.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (o *Increase) SetExecutedAt(executedAt *time.Ticket) {
	o.executedAt = executedAt
}

// Actor returns the actor for dedup mode. Empty string means normal mode.
func (o *Increase) Actor() string {
	return o.actor
}
