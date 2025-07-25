//go:build bench

/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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
 *
 * This file was written with reference to moby/locker.
 *   https://github.com/moby/locker
 */

package bench

import (
	"fmt"
	"math/rand"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/locker"
)

func BenchmarkLocker(b *testing.B) {
	l := locker.New()
	for range b.N {
		l.Lock("test")
		assert.NoError(b, l.Unlock("test"))
	}
}

func BenchmarkLockerParallel(b *testing.B) {
	l := locker.New()
	b.SetParallelism(128)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			l.Lock("test")
			assert.NoError(b, l.Unlock("test"))
		}
	})
}

func BenchmarkLockerMoreKeys(b *testing.B) {
	l := locker.New()
	var keys []string
	for i := range 64 {
		keys = append(keys, strconv.Itoa(i))
	}
	b.SetParallelism(128)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			k := keys[rand.Intn(len(keys))]
			l.Lock(k)
			assert.NoError(b, l.Unlock(k))
		}
	})
}

func BenchmarkRWLocker(b *testing.B) {
	b.SetParallelism(128)

	rates := []int{2, 10, 100, 1000}
	for _, rate := range rates {
		b.Run(fmt.Sprintf("RWLock rate %d", rate), func(b *testing.B) {
			benchmarkRWLockerParallel(rate, b)
		})
	}
}

func benchmarkRWLockerParallel(rate int, b *testing.B) {
	l := locker.New()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if rand.Intn(rate) == 0 {
				l.Lock("test")
				assert.NoError(b, l.Unlock("test"))
			} else {
				l.RLock("test")
				assert.NoError(b, l.RUnlock("test"))
			}
		}
	})
}
