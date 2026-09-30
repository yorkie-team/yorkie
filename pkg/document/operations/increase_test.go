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
	// A JS client sends a fractional delta as a Double primitive. On a Long
	// counter its reverse is the negated Double, as in JS IncreaseOperation.
	// On an Integer counter it is the Integer change the counter made.
	t.Run("execute a Double delta and its reverse", func(t *testing.T) {
		for _, tc := range []struct {
			cntType     crdt.CounterType
			want        string
			reverseType crdt.ValueType
		}{
			{crdt.IntegerCnt, "11", crdt.Integer},
			{crdt.LongCnt, "11", crdt.Double},
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
				assert.Equal(t, tc.reverseType, reverse.Value().(*crdt.Primitive).ValueType())
				_, err = reverse.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
				assert.NoError(t, err)
				assert.Equal(t, `{"cnt":10}`, root.Object().Marshal())
			}
		}
	})

	// An Integer counter adds a Double delta in float64 and wraps the sum, so
	// the negated delta cannot always undo it: 10 + 2^60 rounds away the 10.
	// The reverse records the change the counter actually made instead.
	t.Run("undo a Double delta that wraps an Integer counter", func(t *testing.T) {
		for _, tc := range []struct {
			delta float64
			want  string
		}{
			{1.5, "11"},
			{-1.5, "9"},
			{0x1p31 + 0.5, "-2147483638"},
			{0x1p60, "0"},
			{-0x1p60, "0"},
			{1e20, "1661992960"},
		} {
			root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
			actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

			cntTicket := time.NewTicket(1, 0, actor)
			counter, err := crdt.NewCounter(crdt.IntegerCnt, 10, cntTicket)
			assert.NoError(t, err)
			set := operations.NewSet(time.InitialTicket, "cnt", counter, cntTicket)
			_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
			assert.NoError(t, err)

			incTicket := time.NewTicket(2, 0, actor)
			delta, err := crdt.NewPrimitive(tc.delta, incTicket)
			assert.NoError(t, err)
			inc := operations.NewIncrease(cntTicket, delta, incTicket)
			result, err := inc.Execute(root, operations.OpSourceLocal, time.NewVersionVector())
			assert.NoError(t, err)
			assert.Equal(t, `{"cnt":`+tc.want+`}`, root.Object().Marshal(), "delta %v", tc.delta)

			if assert.NotNil(t, result.Reverse) {
				undo, err := result.Reverse.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
				assert.NoError(t, err)
				assert.Equal(t, `{"cnt":10}`, root.Object().Marshal(), "undo of delta %v", tc.delta)

				// Redo, the reverse of the undo, applies the change again.
				_, err = undo.Reverse.Execute(root, operations.OpSourceUndoRedo, time.NewVersionVector())
				assert.NoError(t, err)
				assert.Equal(t, `{"cnt":`+tc.want+`}`, root.Object().Marshal(), "redo of delta %v", tc.delta)
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

	// The same rule for a delta that is not a number at all: a raw client can
	// push an Increase whose value is any element, and the server stores the
	// change before executing it. It must neither panic nor error off the
	// local path, where the change is already durable.
	t.Run("execute an inapplicable delta as a no-op off the local path", func(t *testing.T) {
		root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
		actor, _ := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")

		cntTicket := time.NewTicket(1, 0, actor)
		counter, err := crdt.NewCounter(crdt.IntegerCnt, 10, cntTicket)
		assert.NoError(t, err)
		set := operations.NewSet(time.InitialTicket, "cnt", counter, cntTicket)
		_, err = set.Execute(root, operations.OpSourceRemote, time.NewVersionVector())
		assert.NoError(t, err)

		incTicket := time.NewTicket(2, 0, actor)
		str, err := crdt.NewPrimitive("1", incTicket)
		assert.NoError(t, err)
		for _, delta := range []crdt.Element{
			// Not a Primitive at all.
			crdt.NewObject(crdt.NewElementRHT(), incTicket),
			// A Primitive, but not a numeric one.
			str,
		} {
			for _, source := range []operations.OpSource{
				operations.OpSourceRemote,
				operations.OpSourceReplay,
			} {
				result, err := operations.NewIncrease(cntTicket, delta, incTicket).
					Execute(root, source, time.NewVersionVector())
				assert.NoError(t, err, "%T on %v", delta, source)
				assert.False(t, result.Observable)
				assert.Equal(t, `{"cnt":10}`, root.Object().Marshal())
			}

			_, err := operations.NewIncrease(cntTicket, delta, incTicket).
				Execute(root, operations.OpSourceLocal, time.NewVersionVector())
			assert.Error(t, err, "%T locally", delta)
		}
	})
}
