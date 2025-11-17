/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package heap_test

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/heap"
)

func TestHeap(t *testing.T) {
	t.Run("min heap basic operations", func(t *testing.T) {
		h := heap.New(0, func(a, b int) bool { return a < b })

		assert.True(t, h.IsEmpty())
		assert.Equal(t, 0, h.Len())

		h.Push(5)
		h.Push(3)
		h.Push(7)
		h.Push(1)
		h.Push(9)

		assert.False(t, h.IsEmpty())
		assert.Equal(t, 5, h.Len())
		assert.Equal(t, 1, h.Peek())

		assert.Equal(t, 1, h.Pop())
		assert.Equal(t, 3, h.Pop())
		assert.Equal(t, 5, h.Pop())
		assert.Equal(t, 7, h.Pop())
		assert.Equal(t, 9, h.Pop())

		assert.True(t, h.IsEmpty())
	})

	t.Run("min heap with fixed size (top-K)", func(t *testing.T) {
		// Create a heap with max size 3 (for top-3 elements)
		h := heap.New(3, func(a, b int) bool { return a < b })

		// Push more than 3 elements
		numbers := []int{5, 2, 8, 1, 9, 3, 7, 4, 6}
		for _, n := range numbers {
			h.Push(n)
		}

		// Heap should only contain top 3 elements
		assert.Equal(t, 3, h.Len())
		assert.True(t, h.IsFull())

		// Get all items and sort them to verify
		items := h.Items()
		sort.Ints(items)

		// Should contain [7, 8, 9] (top 3 from input)
		assert.Equal(t, []int{7, 8, 9}, items)
	})

	t.Run("min heap top-K with custom struct", func(t *testing.T) {
		type Item struct {
			Name  string
			Score int
		}

		// Create a heap to find top-5 items by score
		h := heap.New(5, func(a, b Item) bool { return a.Score < b.Score })

		items := []Item{
			{"A", 10},
			{"B", 5},
			{"C", 20},
			{"D", 15},
			{"E", 8},
			{"F", 25},
			{"G", 12},
			{"H", 3},
			{"I", 18},
		}

		for _, item := range items {
			h.Push(item)
		}

		assert.Equal(t, 5, h.Len())

		// Extract all items
		result := make([]Item, 0, h.Len())
		for !h.IsEmpty() {
			result = append(result, h.Pop())
		}

		// Top 5 scores from input: 25, 20, 18, 15, 12
		// Pop returns in ascending order (min-heap): 12, 15, 18, 20, 25
		assert.Equal(t, 5, len(result))

		// The minimum of top-5 should be 12
		assert.Equal(t, 12, result[0].Score)

		// Verify all scores are >= 12
		for _, item := range result {
			assert.GreaterOrEqual(t, item.Score, 12)
		}
	})

	t.Run("min heap peek operation", func(t *testing.T) {
		h := heap.New(0, func(a, b int) bool { return a < b })

		h.Push(5)
		h.Push(3)
		h.Push(7)

		assert.Equal(t, 3, h.Peek())
		assert.Equal(t, 3, h.Len()) // Peek shouldn't remove element

		h.Pop()
		assert.Equal(t, 5, h.Peek())
	})

	t.Run("clear operation", func(t *testing.T) {
		h := heap.New(0, func(a, b int) bool { return a < b })

		h.Push(1)
		h.Push(2)
		h.Push(3)

		assert.Equal(t, 3, h.Len())

		h.Clear()

		assert.Equal(t, 0, h.Len())
		assert.True(t, h.IsEmpty())
	})

	t.Run("empty heap operations", func(t *testing.T) {
		h := heap.New(0, func(a, b int) bool { return a < b })

		assert.Equal(t, 0, h.Pop())
		assert.Equal(t, 0, h.Peek())
	})

	t.Run("min heap with custom comparison function", func(t *testing.T) {
		// Min heap by string length
		h := heap.New(3, func(a, b string) bool { return len(a) < len(b) })

		words := []string{"hello", "hi", "world", "test", "go", "programming"}
		for _, word := range words {
			h.Push(word)
		}

		assert.Equal(t, 3, h.Len())

		// Should contain 3 longest words
		items := h.Items()
		for _, item := range items {
			// All items should have length >= 5 (hello, world, programming)
			assert.GreaterOrEqual(t, len(item), 5)
		}
	})

	t.Run("max heap basic operations", func(t *testing.T) {
		// Using a > b makes it behave like a max heap
		// Larger values are considered "smaller" and go to the root
		h := heap.New(0, func(a, b int) bool { return a > b })

		assert.True(t, h.IsEmpty())

		h.Push(5)
		h.Push(3)
		h.Push(7)
		h.Push(1)
		h.Push(9)

		assert.False(t, h.IsEmpty())
		assert.Equal(t, 5, h.Len())
		// Peek should return the maximum value (9)
		assert.Equal(t, 9, h.Peek())

		// Pop should return values in descending order (max to min)
		assert.Equal(t, 9, h.Pop())
		assert.Equal(t, 7, h.Pop())
		assert.Equal(t, 5, h.Pop())
		assert.Equal(t, 3, h.Pop())
		assert.Equal(t, 1, h.Pop())

		assert.True(t, h.IsEmpty())
	})

	t.Run("max heap with fixed size (bottom-K)", func(t *testing.T) {
		// Create a heap with max size 3 using a > b
		// This will keep the 3 smallest elements (bottom-3)
		h := heap.New(3, func(a, b int) bool { return a > b })

		// Push more than 3 elements
		numbers := []int{5, 2, 8, 1, 9, 3, 7, 4, 6}
		for _, n := range numbers {
			h.Push(n)
		}

		// Heap should only contain bottom 3 elements
		assert.Equal(t, 3, h.Len())
		assert.True(t, h.IsFull())

		// Get all items and sort them to verify
		items := h.Items()
		sort.Ints(items)

		// Should contain [1, 2, 3] (bottom 3 from input)
		assert.Equal(t, []int{1, 2, 3}, items)

		// Peek should return the maximum of the bottom-3 (which is 3)
		assert.Equal(t, 3, h.Peek())
	})

	t.Run("max heap bottom-K with custom struct", func(t *testing.T) {
		type Item struct {
			Name  string
			Score int
		}

		// Create a heap to find bottom-5 items by score (using a > b)
		h := heap.New(5, func(a, b Item) bool { return a.Score > b.Score })

		items := []Item{
			{"A", 10},
			{"B", 5},
			{"C", 20},
			{"D", 15},
			{"E", 8},
			{"F", 25},
			{"G", 12},
			{"H", 3},
			{"I", 18},
		}

		for _, item := range items {
			h.Push(item)
		}

		assert.Equal(t, 5, h.Len())

		// Extract all items
		result := make([]Item, 0, h.Len())
		for !h.IsEmpty() {
			result = append(result, h.Pop())
		}

		// Bottom 5 scores from input: 3, 5, 8, 10, 12
		// Pop returns in descending order (max-heap): 12, 10, 8, 5, 3
		assert.Equal(t, 5, len(result))

		// The maximum of bottom-5 should be 12
		assert.Equal(t, 12, result[0].Score)

		// Verify all scores are <= 12
		for _, item := range result {
			assert.LessOrEqual(t, item.Score, 12)
		}
	})

	t.Run("max heap peek operation", func(t *testing.T) {
		h := heap.New(0, func(a, b int) bool { return a > b })

		h.Push(5)
		h.Push(3)
		h.Push(7)

		// Peek should return the maximum value
		assert.Equal(t, 7, h.Peek())
		assert.Equal(t, 3, h.Len()) // Peek shouldn't remove element

		h.Pop()                      // Remove 7
		assert.Equal(t, 5, h.Peek()) // Next max is 5
	})
}
