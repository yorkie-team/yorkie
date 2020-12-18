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

func TestPQ(t *testing.T) {
	t.Run("priority queue test", func(t *testing.T) {
		pq := pq.NewPriorityQueue()
		testNums := []int{10, 7, 1, 9, 4, 11, 5, 3, 6, 12, 8, 2}
		for _, testNum := range testNums {
			pq.Push(newTestValue(testNum))
		}
		for _, testNum := range testNums {
			assert.True(t, exist(testNum, pq.Values()))
		}
		assert.Equal(t, 12, pq.Len())
		assert.Equal(t, newTestValue(1), pq.Peek())
		assert.Equal(t, newTestValue(1), pq.Pop())
		assert.Equal(t, 11, pq.Len())
		assert.False(t, exist(1, pq.Values()))
		assert.Equal(t, newTestValue(2), pq.Peek())
		assert.Equal(t, pq.Peek(), pq.Pop())

		pq.Release(newTestValue(3))
		assert.False(t, exist(3, pq.Values()))

		for i := 4; i <= 12; i++ {
			assert.Equal(t, newTestValue(i), pq.Peek())
			assert.Equal(t, newTestValue(i), pq.Pop())
		}

		assert.Equal(t, 0, pq.Len())
	})
}
