//go:build bench

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	gosync "sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/sync"
)

func BenchmarkSync(b *testing.B) {
	b.Run("memory sync 10 test", func(b *testing.B) {
		benchmarkMemorySync(10, b)
	})

	b.Run("memory sync 100 test", func(b *testing.B) {
		benchmarkMemorySync(100, b)
	})

	b.Run("memory sync 1000 test", func(b *testing.B) {
		benchmarkMemorySync(1000, b)
	})

	b.Run("memory sync 10000 test", func(b *testing.B) {
		benchmarkMemorySync(10000, b)
	})
}

func benchmarkMemorySync(cnt int, b *testing.B) {
	for range b.N {
		locker := sync.New()

		sum := 0
		var wg gosync.WaitGroup
		for range cnt {
			wg.Add(1)
			go func() {
				defer wg.Done()

				locker := locker.Locker(sync.Key(b.Name()))
				sum += 1
				locker.Unlock()
			}()
		}
		wg.Wait()
		assert.Equal(b, cnt, sum)
	}
}
