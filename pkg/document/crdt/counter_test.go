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
	"strconv"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestCounter(t *testing.T) {
	t.Run("new counter test", func(t *testing.T) {
		intCntWithInt32Value, err := crdt.NewCounter(crdt.IntegerCnt, int32(math.MaxInt32), time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.IntegerCnt, intCntWithInt32Value.ValueType())

		intCntWithInt64Value, err := crdt.NewCounter(crdt.IntegerCnt, int64(math.MaxInt32+1), time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.IntegerCnt, intCntWithInt64Value.ValueType())

		intCntWithIntValue, err := crdt.NewCounter(crdt.IntegerCnt, math.MaxInt32, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.IntegerCnt, intCntWithIntValue.ValueType())

		intCntWithDoubleValue, err := crdt.NewCounter(crdt.IntegerCnt, 0.5, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.IntegerCnt, intCntWithDoubleValue.ValueType())

		_, err = crdt.NewCounter(crdt.IntegerCnt, "", time.InitialTicket)
		assert.ErrorIs(t, err, crdt.ErrUnsupportedType)

		longCntWithInt32Value, err := crdt.NewCounter(crdt.LongCnt, int32(math.MaxInt32), time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.LongCnt, longCntWithInt32Value.ValueType())

		longCntWithInt64Value, err := crdt.NewCounter(crdt.LongCnt, int64(math.MaxInt32+1), time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.LongCnt, longCntWithInt64Value.ValueType())

		longCntWithIntValue, err := crdt.NewCounter(crdt.LongCnt, math.MaxInt32+1, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.LongCnt, longCntWithIntValue.ValueType())

		longCntWithDoubleValue, err := crdt.NewCounter(crdt.LongCnt, 0.5, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, crdt.LongCnt, longCntWithDoubleValue.ValueType())

		_, err = crdt.NewCounter(crdt.LongCnt, "", time.InitialTicket)
		assert.ErrorIs(t, err, crdt.ErrUnsupportedType)
	})

	t.Run("increase test", func(t *testing.T) {
		var x = 5
		var y int64 = 10
		var z = 3.14
		integer, err := crdt.NewCounter(crdt.IntegerCnt, x, time.InitialTicket)
		assert.NoError(t, err)
		long, err := crdt.NewCounter(crdt.LongCnt, y, time.InitialTicket)
		assert.NoError(t, err)
		double, err := crdt.NewCounter(crdt.IntegerCnt, z, time.InitialTicket)
		assert.NoError(t, err)

		integerOperand, err := crdt.NewPrimitive(x, time.InitialTicket)
		assert.NoError(t, err)
		longOperand, err := crdt.NewPrimitive(y, time.InitialTicket)
		assert.NoError(t, err)
		doubleOperand, err := crdt.NewPrimitive(z, time.InitialTicket)
		assert.NoError(t, err)

		// normal process test
		_, err = integer.Increase(integerOperand)
		assert.NoError(t, err)
		_, err = integer.Increase(longOperand)
		assert.NoError(t, err)
		_, err = integer.Increase(doubleOperand)
		assert.NoError(t, err)
		assert.Equal(t, integer.Marshal(), "23")

		_, err = long.Increase(integerOperand)
		assert.NoError(t, err)
		_, err = long.Increase(longOperand)
		assert.NoError(t, err)
		_, err = long.Increase(doubleOperand)
		assert.NoError(t, err)
		assert.Equal(t, long.Marshal(), "28")

		_, err = double.Increase(integerOperand)
		assert.NoError(t, err)
		_, err = double.Increase(longOperand)
		assert.NoError(t, err)
		_, err = double.Increase(doubleOperand)
		assert.NoError(t, err)
		assert.Equal(t, double.Marshal(), "21")

		// error process test
		unsupportedTypeErrorTest := func(v interface{}) {
			_, err = crdt.NewCounter(crdt.IntegerCnt, v, time.InitialTicket)
			assert.ErrorIs(t, err, crdt.ErrUnsupportedType)
		}
		unsupportedTypeErrorTest("str")
		unsupportedTypeErrorTest(true)
		unsupportedTypeErrorTest([]byte{2})
		unsupportedTypeErrorTest(gotime.Now())
	})

	t.Run("Counter value overflow test", func(t *testing.T) {
		integer, err := crdt.NewCounter(crdt.IntegerCnt, math.MaxInt32, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, integer.ValueType(), crdt.IntegerCnt)

		operand, err := crdt.NewPrimitive(1, time.InitialTicket)
		assert.NoError(t, err)

		_, err = integer.Increase(operand)
		assert.NoError(t, err)
		assert.Equal(t, integer.ValueType(), crdt.IntegerCnt)
		assert.Equal(t, integer.Marshal(), strconv.FormatInt(math.MinInt32, 10))
	})
}
