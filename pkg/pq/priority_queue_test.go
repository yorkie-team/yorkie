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

package pq_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/pq"
)

type testValue struct {
	value int
}

func (value testValue) Less(other pq.Value) bool {
	return value.value < other.(testValue).value
}

func newTestValue(value int) testValue {
	return testValue{
		value: value,
	}
}

func exist(toFind int, values []pq.Value) bool {
	ret := false
	for _, value := range values {
		v := value.(testValue)
		if v.value == toFind {
			ret = true
			break
		}
	}
	return ret
}

func setUpTestNums() *pq.PriorityQueue {
	pq := pq.NewPriorityQueue()
	testNums := []int{10, 7, 1, 9, 4, 11, 5, 3, 6, 12, 8, 2}
	for _, testNum := range testNums {
		pq.Push(NewTestValue(testNum))
	}

	return pq
}

func TestPQ(t *testing.T) {
	t.Run("priority queue push", func(t *testing.T) {
		pq := setUpTestNums()

		for _, testNum := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
			assert.True(t, exist(testNum, pq.Values()))
		}
	})

	t.Run("priority queue peek", func(t *testing.T) {
		pq := setUpTestNums()

		assert.Equal(t, 12, pq.Len())
		assert.Equal(t, NewTestValue(1), pq.Peek())
		assert.Equal(t, 12, pq.Len())
		assert.True(t, exist(1, pq.Values()))
	})

	t.Run("priority queue pop", func(t *testing.T) {
		pq := setUpTestNums()

		assert.Equal(t, NewTestValue(1), pq.Pop())
		assert.Equal(t, pq.Peek(), pq.Pop())
		assert.Equal(t, 10, pq.Len())

		tmp := []int{}
		for pq.Len() != 0 {
			tmp = append(tmp, (pq.Pop()).(testValue).value)
		}

		assert.EqualValues(t, tmp, []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	})

	t.Run("priority queue release", func(t *testing.T) {
		pq := setUpTestNums()

		for i := 1; i <= 12; i++ {
			pq.Release(NewTestValue(i))
			assert.False(t, exist(i, pq.Values()))
		}
		assert.Equal(t, 0, pq.Len())

		pq = setUpTestNums()

		for i := 12; i >= 1; i-- {
			pq.Release(NewTestValue(i))
			assert.False(t, exist(i, pq.Values()))
		}
		assert.Equal(t, 0, pq.Len())

		pq = setUpTestNums()

		pqLen := len(pq.Values())
		pq.Release(NewTestValue(13))
		assert.Equal(t, pqLen, len(pq.Values()))
	})
}
