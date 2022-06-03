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
	"fmt"
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

func exist(toFind int, values []testValue) bool {
	ret := false
	for _, value := range values {
		if value.value == toFind {
			ret = true
			break
		}
	}
	return ret
}

func setUpTestNums() *pq.PriorityQueue[testValue] {
	queue := pq.NewPriorityQueue[testValue]()
	testNums := []int{10, 7, 1, 9, 4, 11, 5, 3, 6, 12, 8, 2}
	for _, testNum := range testNums {
		queue.Push(newTestValue(testNum))
	}

	return queue
}

func TestPQ(t *testing.T) {
	t.Run("priority queue push", func(t *testing.T) {
		queue := setUpTestNums()

		for _, testNum := range []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12} {
			assert.True(t, exist(testNum, queue.Values()))
		}
	})

	t.Run("priority queue peek", func(t *testing.T) {
		queue := setUpTestNums()

		assert.Equal(t, 12, queue.Len())
		assert.Equal(t, newTestValue(1), queue.Peek())
		assert.Equal(t, 12, queue.Len())
		assert.True(t, exist(1, queue.Values()))
	})

	t.Run("priority queue pop", func(t *testing.T) {
		queue := setUpTestNums()

		assert.Equal(t, newTestValue(1), queue.Pop())
		assert.Equal(t, queue.Peek(), queue.Pop())
		assert.Equal(t, 10, queue.Len())

		var tmp []int
		for queue.Len() != 0 {
			tmp = append(tmp, (queue.Pop()).value)
		}

		assert.EqualValues(t, tmp, []int{3, 4, 5, 6, 7, 8, 9, 10, 11, 12})
	})

}

func TestPQRelease(t *testing.T) {
	t.Run("priority queue release", func(t *testing.T) {
		queue := setUpTestNums()
		for i := 1; i <= 12; i++ {
			queue.Release(newTestValue(i))
			assert.False(t, exist(i, queue.Values()))
		}
		assert.Equal(t, 0, queue.Len())

		queue = setUpTestNums()
		for i := 12; i >= 1; i-- {
			queue.Release(newTestValue(i))
			assert.False(t, exist(i, queue.Values()))
		}
		assert.Equal(t, 0, queue.Len())

		queue = setUpTestNums()
		queueLen := len(queue.Values())
		queue.Release(newTestValue(13))
		assert.Equal(t, queueLen, len(queue.Values()))
	})

	t.Run("root node is deleted test", func(t *testing.T) {
		queue := setUpTestNums()
		root := newTestValue(11)
		queue.Release(root)

		expected := "[{1} {3} {2} {4} {8} {5} {7} {10} {6} {12} {9}]"
		assert.Equal(t, expected, fmt.Sprint(queue.Values()))
	})

	t.Run("if parent node is deleted", func(t *testing.T) {
		queue := setUpTestNums()
		parent := newTestValue(5)

		queue.Release(parent)

		expected := "[{1} {3} {2} {4} {8} {11} {7} {10} {6} {12} {9}]"
		assert.Equal(t, expected, fmt.Sprint(queue.Values()))
	})

	t.Run("if leaf node is deleted", func(t *testing.T) {
		queue := setUpTestNums()
		leaf := newTestValue(9)

		queue.Release(leaf)

		expected := "[{1} {3} {2} {4} {8} {5} {7} {10} {6} {12} {11}]"
		assert.Equal(t, expected, fmt.Sprint(queue.Values()))
	})

	t.Run("if a heap has one node", func(t *testing.T) {
		queue := pq.NewPriorityQueue[testValue]()
		node := newTestValue(0)

		queue.Push(node)
		queue.Release(node)

		assert.Equal(t, 0, queue.Len())
	})
}
