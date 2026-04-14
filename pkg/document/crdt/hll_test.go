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

package crdt_test

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
)

func TestHLL(t *testing.T) {
	t.Run("new HLL has zero count", func(t *testing.T) {
		hll := crdt.NewHLL()
		assert.Equal(t, uint64(0), hll.Count())
	})

	t.Run("add single element returns true", func(t *testing.T) {
		hll := crdt.NewHLL()
		added := hll.Add("user-1")
		assert.True(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("add duplicate element returns false", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		added := hll.Add("user-1")
		assert.False(t, added)
		assert.Equal(t, uint64(1), hll.Count())
	})

	t.Run("count many unique elements within error margin", func(t *testing.T) {
		hll := crdt.NewHLL()
		n := 100000
		for i := 0; i < n; i++ {
			hll.Add(fmt.Sprintf("user-%d", i))
		}
		count := hll.Count()
		errorRate := math.Abs(float64(count)-float64(n)) / float64(n)
		assert.Less(t, errorRate, 0.05) // within 5%
	})

	t.Run("merge two HLLs", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll1.Add("user-2")

		hll2.Add("user-2")
		hll2.Add("user-3")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(3), hll1.Count())
	})

	t.Run("merge is idempotent", func(t *testing.T) {
		hll1 := crdt.NewHLL()
		hll2 := crdt.NewHLL()

		hll1.Add("user-1")
		hll2.Add("user-1")

		hll1.Merge(hll2)
		assert.Equal(t, uint64(1), hll1.Count())
	})

	t.Run("serialize and restore", func(t *testing.T) {
		hll := crdt.NewHLL()
		hll.Add("user-1")
		hll.Add("user-2")
		hll.Add("user-3")
		originalCount := hll.Count()

		bytes := hll.Bytes()
		assert.Equal(t, 1<<14, len(bytes)) // precision 14 = 16384 registers

		restored := crdt.NewHLL()
		err := restored.Restore(bytes)
		assert.NoError(t, err)
		assert.Equal(t, originalCount, restored.Count())
	})
}
