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

package backend

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// fanOut manages concurrent fan-out/fan-in execution with a server-wide
// concurrency limit.
type fanOut struct {
	semaphore *semaphore.Weighted
}

// newFanOut creates a new fanOut with the given max concurrency.
func newFanOut(maxConcurrency int64) *fanOut {
	return &fanOut{
		semaphore: semaphore.NewWeighted(maxConcurrency),
	}
}

// FanOut executes fn for each task concurrently, respecting the Backend's
// concurrency limit. It collects all results and returns them along with
// the first error encountered (if any).
//
// Unlike Backend.Go (fire-and-forget), FanOut is request-scoped:
//   - The caller receives results back.
//   - Context cancellation is respected.
//   - Server-wide concurrency is bounded by the semaphore.
func FanOut[K comparable, V any, R any](
	ctx context.Context,
	be *Backend,
	tasks map[K]V,
	fn func(ctx context.Context, key K, values V) ([]R, error),
) ([]R, error) {
	if len(tasks) == 0 {
		return nil, nil
	}

	type result struct {
		values []R
		err    error
	}

	resultCh := make(chan result, len(tasks))

	var wg sync.WaitGroup
	for k, v := range tasks {
		wg.Add(1)
		go func() {
			defer wg.Done()

			if err := be.fanOut.semaphore.Acquire(ctx, 1); err != nil {
				resultCh <- result{err: err}
				return
			}
			defer be.fanOut.semaphore.Release(1)

			values, err := fn(ctx, k, v)
			resultCh <- result{values: values, err: err}
		}()
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var collected []R
	var firstErr error
	for res := range resultCh {
		if res.err != nil && firstErr == nil {
			firstErr = res.err
		}
		if res.err == nil {
			collected = append(collected, res.values...)
		}
	}

	return collected, firstErr
}
