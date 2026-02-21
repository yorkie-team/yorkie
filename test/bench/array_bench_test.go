//go:build bench

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

package bench

import (
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// OpType represents the type of array operation.
type OpType uint8

const (
	Insert OpType = iota
	Delete
	Move
	Set
	Get
)

// newArray creates a json.Array with a minimal change.Context for benchmarking.
func newArray() *json.Array {
	root := crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	ctx := change.NewContext(change.InitialID(), "", root)
	return json.NewArray(ctx, crdt.NewArray(crdt.NewRGATreeList(), ctx.IssueTimeTicket()))
}

// setupBenchArray creates a json.Array pre-populated with `size` integers.
func setupBenchArray(size int) *json.Array {
	arr := newArray()
	for i := range size {
		arr.AddInteger(i)
	}
	return arr
}

// generateOps creates a deterministic sequence of operations for benchmarking.
// Distribution: 40% Insert, 20% Delete, 15% Move, 15% Set, 10% Get.
func generateOps(rng *rand.Rand, count int) []OpType {
	ops := make([]OpType, count)
	for i := range ops {
		r := rng.Intn(100)
		switch {
		case r < 40:
			ops[i] = Insert
		case r < 60:
			ops[i] = Delete
		case r < 75:
			ops[i] = Move
		case r < 90:
			ops[i] = Set
		default:
			ops[i] = Get
		}
	}
	return ops
}

func BenchmarkArray(b *testing.B) {
	b.Run("sequential insert 10000", func(b *testing.B) {
		benchmarkArraySequentialInsert(10000, b)
	})
	b.Run("sequential insert 100000", func(b *testing.B) {
		benchmarkArraySequentialInsert(100000, b)
	})
	b.Run("random insert 10000", func(b *testing.B) {
		benchmarkArrayRandomInsert(10000, b)
	})
	b.Run("random insert 100000", func(b *testing.B) {
		benchmarkArrayRandomInsert(100000, b)
	})
	b.Run("random delete 10000", func(b *testing.B) {
		benchmarkArrayRandomDelete(10000, b)
	})
	b.Run("random delete 100000", func(b *testing.B) {
		benchmarkArrayRandomDelete(100000, b)
	})
	b.Run("random access 10000", func(b *testing.B) {
		benchmarkArrayRandomAccess(10000, b)
	})
	b.Run("random access 100000", func(b *testing.B) {
		benchmarkArrayRandomAccess(100000, b)
	})
	b.Run("random move 10000", func(b *testing.B) {
		benchmarkArrayRandomMove(10000, b)
	})
	b.Run("random set 10000", func(b *testing.B) {
		benchmarkArrayRandomSet(10000, b)
	})
	b.Run("random set 100000", func(b *testing.B) {
		benchmarkArrayRandomSet(100000, b)
	})
	b.Run("mixed workload 10000", func(b *testing.B) {
		benchmarkArrayMixedWorkload(10000, b)
	})
	b.Run("mixed workload 100000", func(b *testing.B) {
		benchmarkArrayMixedWorkload(100000, b)
	})
	b.Run("heavy delete then insert 50000", func(b *testing.B) {
		benchmarkArrayHeavyDeleteThenInsert(50000, b)
	})
	b.Run("move heavy 10000", func(b *testing.B) {
		benchmarkArrayMoveHeavy(10000, b)
	})
}

func benchmarkArraySequentialInsert(size int, b *testing.B) {
	for b.Loop() {
		arr := newArray()
		for i := range size {
			arr.AddInteger(i)
		}
		assert.Equal(b, size, arr.Len())
	}
}

func benchmarkArrayRandomInsert(size int, b *testing.B) {
	for b.Loop() {
		rng := rand.New(rand.NewSource(42))
		arr := newArray()
		arr.AddInteger(0)
		for i := 1; i < size; i++ {
			idx := rng.Intn(arr.Len())
			arr.InsertIntegerAfter(idx, i)
		}
		assert.Equal(b, size, arr.Len())
	}
}

func benchmarkArrayRandomDelete(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		b.StartTimer()

		for arr.Len() > 0 {
			idx := rng.Intn(arr.Len())
			arr.Delete(idx)
		}
		assert.Equal(b, 0, arr.Len())
	}
}

func benchmarkArrayRandomAccess(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		b.StartTimer()

		for range size {
			idx := rng.Intn(arr.Len())
			_ = arr.Get(idx)
		}
		assert.Equal(b, size, arr.Len())
	}
}

func benchmarkArrayRandomMove(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		moveCount := size / 2
		b.StartTimer()

		for range moveCount {
			n := arr.Len()
			from := rng.Intn(n)
			to := rng.Intn(n)
			for to == from {
				to = rng.Intn(n)
			}
			arr.MoveAfterByIndex(to, from)
		}
		assert.Equal(b, size, arr.Len())
	}
}

func benchmarkArrayRandomSet(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		b.StartTimer()

		for range size {
			idx := rng.Intn(arr.Len())
			arr.SetInteger(idx, rng.Int())
		}
		assert.Equal(b, size, arr.Len())
	}
}

func benchmarkArrayMixedWorkload(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		ops := generateOps(rng, size)
		b.StartTimer()

		for _, op := range ops {
			n := arr.Len()
			if n == 0 {
				arr.AddInteger(rng.Int())
				continue
			}
			switch op {
			case Insert:
				idx := rng.Intn(n)
				arr.InsertIntegerAfter(idx, rng.Int())
			case Delete:
				idx := rng.Intn(n)
				arr.Delete(idx)
			case Move:
				if n < 2 {
					arr.AddInteger(rng.Int())
					continue
				}
				from := rng.Intn(n)
				to := rng.Intn(n)
				for to == from {
					to = rng.Intn(n)
				}
				arr.MoveAfterByIndex(to, from)
			case Set:
				idx := rng.Intn(n)
				arr.SetInteger(idx, rng.Int())
			case Get:
				idx := rng.Intn(n)
				_ = arr.Get(idx)
			}
		}
		assert.True(b, arr.Len() > 0)
	}
}

func benchmarkArrayHeavyDeleteThenInsert(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		b.StartTimer()

		deleteCount := size / 2
		for range deleteCount {
			idx := rng.Intn(arr.Len())
			arr.Delete(idx)
		}
		remaining := arr.Len()
		for range deleteCount {
			idx := rng.Intn(arr.Len())
			arr.InsertIntegerAfter(idx, rng.Int())
		}
		assert.Equal(b, remaining+deleteCount, arr.Len())
	}
}

func benchmarkArrayMoveHeavy(size int, b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		arr := setupBenchArray(size)
		rng := rand.New(rand.NewSource(42))
		b.StartTimer()

		for range size {
			n := arr.Len()
			targetIdx := rng.Intn(n)
			target := arr.Get(targetIdx)
			r := rng.Intn(3)
			switch r {
			case 0:
				arr.MoveFront(target.CreatedAt())
			case 1:
				arr.MoveLast(target.CreatedAt())
			case 2:
				to := rng.Intn(n)
				for to == targetIdx {
					to = rng.Intn(n)
				}
				arr.MoveAfterByIndex(to, targetIdx)
			}
		}
		assert.Equal(b, size, arr.Len())
	}
}
