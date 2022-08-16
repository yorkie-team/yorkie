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

package crdt_test

import (
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestCounter(t *testing.T) {
	t.Run("new counter test", func(t *testing.T) {
		integer := crdt.NewCounter(math.MaxInt32, time.InitialTicket)
		assert.Equal(t, crdt.IntegerCnt, integer.ValueType())

		long := crdt.NewCounter(math.MaxInt32+1, time.InitialTicket)
		assert.Equal(t, crdt.LongCnt, long.ValueType())

		double := crdt.NewCounter(0.5, time.InitialTicket)
		assert.Equal(t, crdt.DoubleCnt, double.ValueType())
	})

	t.Run("increase test", func(t *testing.T) {
		var x = 5
		var y int64 = 10
		var z = 3.14
		integer := crdt.NewCounter(x, time.InitialTicket)
		long := crdt.NewCounter(y, time.InitialTicket)
		double := crdt.NewCounter(z, time.InitialTicket)

		integerOperand := crdt.NewPrimitive(x, time.InitialTicket)
		longOperand := crdt.NewPrimitive(y, time.InitialTicket)
		doubleOperand := crdt.NewPrimitive(z, time.InitialTicket)

		// normal process test
		integer.Increase(integerOperand)
		integer.Increase(longOperand)
		integer.Increase(doubleOperand)
		assert.Equal(t, integer.Marshal(), "23")

		long.Increase(integerOperand)
		long.Increase(longOperand)
		long.Increase(doubleOperand)
		assert.Equal(t, long.Marshal(), "28")

		double.Increase(integerOperand)
		double.Increase(longOperand)
		double.Increase(doubleOperand)
		assert.Equal(t, double.Marshal(), "21.280000")

		// error process test
		// TODO: it should be modified to error check
		// when 'Remove panic from server code (#50)' is completed.
		unsupportedTypePanicTest := func() {
			r := recover()
			assert.NotNil(t, r)
			assert.Equal(t, r, "unsupported type")
		}
		unsupportedTest := func(v interface{}) {
			defer unsupportedTypePanicTest()
			crdt.NewCounter(v, time.InitialTicket)
		}
		unsupportedTest("str")
		unsupportedTest(true)
		unsupportedTest([]byte{2})
		unsupportedTest(gotime.Now())

		assert.Equal(t, integer.Marshal(), "23")
		assert.Equal(t, long.Marshal(), "28")
		assert.Equal(t, double.Marshal(), "21.280000")
	})

	t.Run("Counter's value type changed Integer to Long test", func(t *testing.T) {
		integer := crdt.NewCounter(math.MaxInt32, time.InitialTicket)
		assert.Equal(t, integer.ValueType(), crdt.IntegerCnt)

		operand := crdt.NewPrimitive(1, time.InitialTicket)
		integer.Increase(operand)
		assert.Equal(t, integer.ValueType(), crdt.LongCnt)
	})
}
