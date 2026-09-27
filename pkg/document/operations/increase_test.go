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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestIncrease(t *testing.T) {
	// A JS client sends a fractional delta as a Double primitive. Its
	// reverse is the negated Double, as in JS IncreaseOperation.
	t.Run("execute a Double delta and its reverse", func(t *testing.T) {
		for _, tc := range []struct {
			cntType crdt.CounterType
			want    string
		}{
			{crdt.IntegerCnt, "11"},
			{crdt.LongCnt, "11"},
		} {
			root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
			actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

			cntTicket := time.NewTicket(1, 0, actor)
			counter, err := crdt.NewCounter(tc.cntType, 10, cntTicket)
			assert.NoError(t, err)
			set := operations.NewSet(time.InitialTicket, "cnt", counter, cntTicket)
			_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
			assert.NoError(t, err)

			incTicket := time.NewTicket(2, 0, actor)
			delta, err := crdt.NewPrimitive(1.5, incTicket)
			assert.NoError(t, err)
			inc := operations.NewIncrease(cntTicket, delta, incTicket)
			result, err := inc.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
			assert.NoError(t, err)
			assert.Equal(t, `{"cnt":`+tc.want+`}`, root.Object().Marshal())

			if assert.NotNil(t, result.Reverse) {
				reverse := result.Reverse.(*operations.Increase)
				assert.Equal(t, crdt.Double, reverse.Value().(*crdt.Primitive).ValueType())
				_, err = reverse.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
				assert.NoError(t, err)
				assert.Equal(t, `{"cnt":10}`, root.Object().Marshal())
			}
		}
	})

	// A remote apply and a server replay discard the reverse, so they do not
	// build one: a reverse that cannot be built must not fail the apply.
	t.Run("remote sources skip the reverse", func(t *testing.T) {
		for _, source := range []operations.OpSource{operations.OpSourceRemote, operations.OpSourceReplay} {
			root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
			actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

			cntTicket := time.NewTicket(1, 0, actor)
			counter, err := crdt.NewCounter(crdt.IntegerCnt, 10, cntTicket)
			assert.NoError(t, err)
			set := operations.NewSet(time.InitialTicket, "cnt", counter, cntTicket)
			_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
			assert.NoError(t, err)

			incTicket := time.NewTicket(2, 0, actor)
			delta, err := crdt.NewPrimitive(1.5, incTicket)
			assert.NoError(t, err)
			result, err := operations.NewIncrease(cntTicket, delta, incTicket).
				Execute(root, source, time.NewVersionVector())
			assert.NoError(t, err)
			assert.Nil(t, result.Reverse)
			assert.Equal(t, `{"cnt":11}`, root.Object().Marshal())
		}
	})

	// A raw client can push an Increase carrying a NaN or an infinity. The
	// server stores the change before executing it, so an apply that failed
	// would break every later replay of the document: the delta is dropped
	// instead, on every source, so all replicas still agree.
	t.Run("execute a non-finite Double delta as a no-op", func(t *testing.T) {
		for _, source := range []operations.OpSource{
			operations.OpSourceLocal,
			operations.OpSourceRemote,
			operations.OpSourceReplay,
			operations.OpSourceUndoRedo,
		} {
			for _, d := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
				for _, cntType := range []crdt.CounterType{crdt.IntegerCnt, crdt.LongCnt} {
					root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
					actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

					cntTicket := time.NewTicket(1, 0, actor)
					counter, err := crdt.NewCounter(cntType, 10, cntTicket)
					assert.NoError(t, err)
					set := operations.NewSet(time.InitialTicket, "cnt", counter, cntTicket)
					_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
					assert.NoError(t, err)

					incTicket := time.NewTicket(2, 0, actor)
					delta, err := crdt.NewPrimitive(d, incTicket)
					assert.NoError(t, err)
					_, err = operations.NewIncrease(cntTicket, delta, incTicket).
						Execute(root, source, time.NewVersionVector())
					assert.NoError(t, err, "%v on %v", d, source)
					assert.Equal(t, `{"cnt":10}`, root.Object().Marshal())
				}
			}
		}
	})
}
