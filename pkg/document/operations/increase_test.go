/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package operations_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestIncreaseRejectsNonPrimitiveValue pins the refusal of an Increase whose
// delta is not a Primitive.
//
// fromIncrease decodes the delta with the same fromElement a Set uses, which
// accepts every element type, so a crafted change can pair a Counter with a
// JSON_OBJECT delta. Execute used to assert the type unchecked, which panics
// -- on the server, inside a goroutine nothing recovers, and again on every
// later replay once the change is stored. It has to be an error instead.
func TestIncreaseRejectsNonPrimitiveValue(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	assert.NoError(t, err)

	counter, err := crdt.NewCounter(crdt.IntegerCnt, int32(0), time.NewTicket(1, 0, actor))
	assert.NoError(t, err)

	obj := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	obj.Set("c", counter)
	root := crdt.NewRoot(obj)

	op := operations.NewIncrease(
		counter.CreatedAt(),
		crdt.NewObject(crdt.NewElementRHT(), time.NewTicket(2, 0, actor)),
		time.NewTicket(3, 0, actor),
	)

	assert.NotPanics(t, func() {
		_, err = op.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.ErrorIs(t, err, operations.ErrNotApplicableDataType)
	})
}
