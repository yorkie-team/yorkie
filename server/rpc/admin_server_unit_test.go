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

package rpc

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/sync/semaphore"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func TestGroupByFirstKeyPath(t *testing.T) {
	t.Run("single key single segment", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{"room-1"})
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Equal(t, []key.Key{key.Key("room-1")}, groups["room-1"])
	})

	t.Run("single key multi segment", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{"room-1.section-1"})
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Equal(t, []key.Key{key.Key("room-1.section-1")}, groups["room-1"])
	})

	t.Run("multiple keys same first path", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{
			"room-1.section-1",
			"room-1.section-2",
		})
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Len(t, groups["room-1"], 2)
		assert.Contains(t, groups["room-1"], key.Key("room-1.section-1"))
		assert.Contains(t, groups["room-1"], key.Key("room-1.section-2"))
	})

	t.Run("multiple keys different first paths", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{
			"room-1.section-1",
			"room-2.section-1",
			"room-3.section-1",
		})
		assert.NoError(t, err)
		assert.Len(t, groups, 3)
		assert.Len(t, groups["room-1"], 1)
		assert.Len(t, groups["room-2"], 1)
		assert.Len(t, groups["room-3"], 1)
	})

	t.Run("three level deep key", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{"room-1.section-1.user-1"})
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Equal(t, []key.Key{key.Key("room-1.section-1.user-1")}, groups["room-1"])
	})

	t.Run("mixed depths same first path", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{
			"room-1",
			"room-1.section-1",
			"room-1.section-1.user-1",
		})
		assert.NoError(t, err)
		assert.Len(t, groups, 1)
		assert.Len(t, groups["room-1"], 3)
	})

	t.Run("empty input", func(t *testing.T) {
		groups, err := groupByFirstKeyPath([]string{})
		assert.NoError(t, err)
		assert.Empty(t, groups)
	})

	t.Run("invalid key empty string returns error", func(t *testing.T) {
		_, err := groupByFirstKeyPath([]string{""})
		assert.Error(t, err)
	})

	t.Run("invalid key starting with dot returns error", func(t *testing.T) {
		_, err := groupByFirstKeyPath([]string{".room-1"})
		assert.Error(t, err)
	})

	t.Run("invalid key ending with dot returns error", func(t *testing.T) {
		_, err := groupByFirstKeyPath([]string{"room-1."})
		assert.Error(t, err)
	})
}

func TestMakeChannelSessionCountCacheKey(t *testing.T) {
	t.Run("basic key with includeSubPath false", func(t *testing.T) {
		result := makeChannelSessionCountCacheKey(
			types.ID("proj-1"), key.Key("room-1"), false,
		)
		assert.Equal(t, "proj-1:room-1:false", result)
	})

	t.Run("basic key with includeSubPath true", func(t *testing.T) {
		result := makeChannelSessionCountCacheKey(
			types.ID("proj-1"), key.Key("room-1"), true,
		)
		assert.Equal(t, "proj-1:room-1:true", result)
	})

	t.Run("hierarchical key", func(t *testing.T) {
		result := makeChannelSessionCountCacheKey(
			types.ID("proj-2"), key.Key("room-1.section-1.user-1"), true,
		)
		assert.Equal(t, "proj-2:room-1.section-1.user-1:true", result)
	})

	t.Run("different projects produce different keys", func(t *testing.T) {
		key1 := makeChannelSessionCountCacheKey(types.ID("proj-A"), key.Key("room-1"), false)
		key2 := makeChannelSessionCountCacheKey(types.ID("proj-B"), key.Key("room-1"), false)
		assert.NotEqual(t, key1, key2)
	})

	t.Run("different includeSubPath produce different keys", func(t *testing.T) {
		key1 := makeChannelSessionCountCacheKey(types.ID("proj-1"), key.Key("room-1"), false)
		key2 := makeChannelSessionCountCacheKey(types.ID("proj-1"), key.Key("room-1"), true)
		assert.NotEqual(t, key1, key2)
	})
}

func TestSemaphoreFanOutPattern(t *testing.T) {
	t.Run("semaphore limits concurrent operations", func(t *testing.T) {
		const maxConcurrent = 3
		const totalGroups = 10
		sem := semaphore.NewWeighted(int64(maxConcurrent))
		ctx := context.Background()

		var peakConcurrency atomic.Int32
		var currentConcurrency atomic.Int32

		type groupResult struct {
			value int
			err   error
		}
		resultCh := make(chan groupResult, totalGroups)

		var wg sync.WaitGroup
		for i := range totalGroups {
			wg.Add(1)
			go func() {
				defer wg.Done()

				if err := sem.Acquire(ctx, 1); err != nil {
					resultCh <- groupResult{err: err}
					return
				}
				defer sem.Release(1)

				current := currentConcurrency.Add(1)
				for {
					old := peakConcurrency.Load()
					if current <= old || peakConcurrency.CompareAndSwap(old, current) {
						break
					}
				}

				time.Sleep(10 * time.Millisecond)
				currentConcurrency.Add(-1)

				resultCh <- groupResult{value: i}
			}()
		}

		go func() { wg.Wait(); close(resultCh) }()

		var results []int
		for res := range resultCh {
			assert.NoError(t, res.err)
			results = append(results, res.value)
		}

		assert.Len(t, results, totalGroups)
		assert.LessOrEqual(t, int(peakConcurrency.Load()), maxConcurrent)
	})

	t.Run("context cancellation unblocks semaphore acquire", func(t *testing.T) {
		sem := semaphore.NewWeighted(1)
		ctx, cancel := context.WithCancel(context.Background())

		type groupResult struct {
			id  int
			err error
		}
		resultCh := make(chan groupResult, 5)

		// Hold the semaphore to block subsequent goroutines.
		err := sem.Acquire(ctx, 1)
		assert.NoError(t, err)

		var wg sync.WaitGroup
		for i := range 5 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := sem.Acquire(ctx, 1); err != nil {
					resultCh <- groupResult{id: i, err: err}
					return
				}
				defer sem.Release(1)
				resultCh <- groupResult{id: i}
			}()
		}

		// Give goroutines time to block on Acquire.
		time.Sleep(50 * time.Millisecond)

		// Cancel the context — all blocked goroutines should unblock with error.
		cancel()

		go func() { wg.Wait(); close(resultCh) }()

		var errCount int
		for res := range resultCh {
			if res.err != nil {
				errCount++
				assert.ErrorIs(t, res.err, context.Canceled)
			}
		}
		assert.Equal(t, 5, errCount)

		sem.Release(1)
	})

	t.Run("context deadline unblocks semaphore acquire", func(t *testing.T) {
		sem := semaphore.NewWeighted(1)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		// Hold the semaphore with a non-cancellable context.
		err := sem.Acquire(context.Background(), 1)
		assert.NoError(t, err)

		type groupResult struct {
			err error
		}
		resultCh := make(chan groupResult, 3)

		var wg sync.WaitGroup
		for range 3 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if err := sem.Acquire(ctx, 1); err != nil {
					resultCh <- groupResult{err: err}
					return
				}
				defer sem.Release(1)
				resultCh <- groupResult{}
			}()
		}

		go func() { wg.Wait(); close(resultCh) }()

		for res := range resultCh {
			assert.Error(t, res.err)
			assert.ErrorIs(t, res.err, context.DeadlineExceeded)
		}

		sem.Release(1)
	})

	t.Run("all goroutines complete even on context cancellation", func(t *testing.T) {
		const totalGroups = 20
		sem := semaphore.NewWeighted(2)
		ctx, cancel := context.WithCancel(context.Background())

		var completedCount atomic.Int32

		type groupResult struct {
			err error
		}
		resultCh := make(chan groupResult, totalGroups)

		var wg sync.WaitGroup
		for range totalGroups {
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer completedCount.Add(1)

				if err := sem.Acquire(ctx, 1); err != nil {
					resultCh <- groupResult{err: err}
					return
				}
				defer sem.Release(1)

				time.Sleep(5 * time.Millisecond)
				resultCh <- groupResult{}
			}()
		}

		// Cancel early while some goroutines are still running.
		time.Sleep(15 * time.Millisecond)
		cancel()

		go func() { wg.Wait(); close(resultCh) }()

		var collected int
		for range resultCh {
			collected++
		}

		assert.Equal(t, totalGroups, collected)
		assert.Equal(t, int32(totalGroups), completedCount.Load())
	})

	t.Run("fan-out fan-in collects all results and tracks first error", func(t *testing.T) {
		sem := semaphore.NewWeighted(10)
		ctx := context.Background()

		type groupResult struct {
			value int
			err   error
		}
		const totalGroups = 10
		resultCh := make(chan groupResult, totalGroups)

		var wg sync.WaitGroup
		for i := range totalGroups {
			wg.Add(1)
			go func() {
				defer wg.Done()

				if err := sem.Acquire(ctx, 1); err != nil {
					resultCh <- groupResult{err: err}
					return
				}
				defer sem.Release(1)

				if i == 5 {
					resultCh <- groupResult{err: fmt.Errorf("group %d failed", i)}
					return
				}
				resultCh <- groupResult{value: i}
			}()
		}

		go func() { wg.Wait(); close(resultCh) }()

		var values []int
		var firstErr error
		for res := range resultCh {
			if res.err != nil && firstErr == nil {
				firstErr = res.err
			}
			if res.err == nil {
				values = append(values, res.value)
			}
		}

		assert.Error(t, firstErr)
		assert.Len(t, values, totalGroups-1)
	})
}
