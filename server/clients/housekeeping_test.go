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

package clients

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// testCtx returns a context with the default logger bound. dispatchDeactivate
// uses logging.From(ctx) on error paths; without a bound logger the default
// is nil and the call panics.
func testCtx() context.Context {
	return logging.With(context.Background(), logging.DefaultLogger())
}

func makeCandidates(n int) []CandidatePair {
	pairs := make([]CandidatePair, n)
	for i := 0; i < n; i++ {
		pairs[i] = CandidatePair{
			Project: &types.Project{ID: types.ID("project")},
			Client: &database.ClientInfo{
				ID:        types.ID(fmt.Sprintf("%024d", i+1)),
				ProjectID: types.ID("project"),
			},
		}
	}
	return pairs
}

func TestDispatchDeactivate_SequentialCountsSuccesses(t *testing.T) {
	candidates := makeCandidates(10)
	var calls atomic.Int32

	count := dispatchDeactivate(testCtx(), candidates, 1, func(_ CandidatePair) error {
		calls.Add(1)
		return nil
	})

	assert.Equal(t, 10, count)
	assert.Equal(t, int32(10), calls.Load())
}

func TestDispatchDeactivate_ParallelCountsSuccesses(t *testing.T) {
	candidates := makeCandidates(100)
	var calls atomic.Int32

	count := dispatchDeactivate(testCtx(), candidates, 16, func(_ CandidatePair) error {
		calls.Add(1)
		return nil
	})

	assert.Equal(t, 100, count)
	assert.Equal(t, int32(100), calls.Load())
}

func TestDispatchDeactivate_SkipsErrorsAndContinues(t *testing.T) {
	candidates := makeCandidates(10)
	wantErr := errors.New("simulated deactivate failure")

	for _, concurrency := range []int{1, 4} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			var calls atomic.Int32
			count := dispatchDeactivate(testCtx(), candidates, concurrency, func(c CandidatePair) error {
				calls.Add(1)
				// fail every odd-indexed candidate
				if c.Client.ID[len(c.Client.ID)-1]%2 == 1 {
					return wantErr
				}
				return nil
			})

			assert.Equal(t, int32(len(candidates)), calls.Load(), "every candidate should be attempted")
			// half succeed (indices 1,3,5,... → ID last char '1','3',... → odd → fail; so even ones succeed)
			assert.Less(t, count, len(candidates))
			assert.Greater(t, count, 0)
		})
	}
}

func TestDispatchDeactivate_ConcurrencyBoundedBySemaphore(t *testing.T) {
	const total = 200
	const concurrency = 8

	candidates := makeCandidates(total)
	var inflight atomic.Int32
	var maxInflight atomic.Int32
	var mu sync.Mutex

	dispatchDeactivate(testCtx(), candidates, concurrency, func(_ CandidatePair) error {
		cur := inflight.Add(1)
		// track max observed in-flight
		mu.Lock()
		if cur > maxInflight.Load() {
			maxInflight.Store(cur)
		}
		mu.Unlock()
		// hold the slot briefly so we see actual concurrency
		time.Sleep(2 * time.Millisecond)
		inflight.Add(-1)
		return nil
	})

	got := maxInflight.Load()
	assert.LessOrEqual(t, got, int32(concurrency), "max in-flight must not exceed concurrency")
	assert.Greater(t, got, int32(1), "with parallel dispatch we expect >1 in-flight at some point")
}

func TestDispatchDeactivate_ContextCancelStops(t *testing.T) {
	candidates := makeCandidates(50)
	cancelCtx, cancel := context.WithCancel(testCtx())
	cancel()
	ctx := cancelCtx

	var calls atomic.Int32
	count := dispatchDeactivate(ctx, candidates, 4, func(_ CandidatePair) error {
		calls.Add(1)
		return nil
	})

	// On a cancelled context, the semaphore.Acquire fails for the first
	// candidate, so dispatch breaks out before any goroutine runs.
	assert.Equal(t, 0, count)
	assert.Equal(t, int32(0), calls.Load())
}

func TestDispatchDeactivate_NoCandidates(t *testing.T) {
	for _, concurrency := range []int{0, 1, 16} {
		t.Run(fmt.Sprintf("concurrency=%d", concurrency), func(t *testing.T) {
			count := dispatchDeactivate(testCtx(), nil, concurrency, func(_ CandidatePair) error {
				t.Fatal("should not be called")
				return nil
			})
			assert.Equal(t, 0, count)
		})
	}
}
