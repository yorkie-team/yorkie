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

// Package limit provide event timing control components
package limit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestSynchronousExecute verifies that synchronous calls to the throttler
// execute the callback immediately without any delays.
func TestSynchronousExecute(t *testing.T) {
	ctx := context.Background()
	const throttleWindow = 10 * time.Millisecond

	t.Run("Single call executes callback exactly once", func(t *testing.T) {
		th := New(throttleWindow)
		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		err := th.Execute(ctx, callback)
		assert.NoError(t, err)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
	})

	t.Run("Ten consecutive calls execute callback each time", func(t *testing.T) {
		th := New(throttleWindow)
		const numExecute = 10
		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		for range numExecute {
			assert.NoError(t, th.Execute(ctx, callback))
		}
		assert.Equal(t, int32(numExecute), atomic.LoadInt32(&callCount))
	})
}

// TestConcurrentExecute verifies that the throttler behaves as expected under concurrent invocations.
func TestConcurrentExecute(t *testing.T) {
	ctx := context.Background()
	const throttleWindow = 100 * time.Millisecond

	// In this test, many concurrent calls are made. The throttler should execute one immediate call
	// and then schedule a trailing call, resulting in exactly two executions.
	t.Run("Concurrent calls result in one immediate and one trailing execution", func(t *testing.T) {
		th := New(throttleWindow)
		const numRoutines = 1000
		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		var wg sync.WaitGroup
		wg.Add(numRoutines)

		for range numRoutines {
			go func() {
				assert.NoError(t, th.Execute(ctx, callback))
				wg.Done()
			}()
		}

		wg.Wait()
		// Expect exactly one immediate and one trailing callback execution.
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
	})

	// This test simulates a continuous stream of events.
	// It triggers multiple concurrent calls at a regular interval and checks that throttling
	// limits the total number of callback invocations to one per window plus one trailing call.
	t.Run("Throttling over continuous event stream", func(t *testing.T) {
		const (
			numWindows     = 10
			eventPerWindow = 100
			numRoutines    = 10
		)
		totalDuration := throttleWindow * time.Duration(numWindows)
		interval := throttleWindow / time.Duration(eventPerWindow)

		th := New(throttleWindow)

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		timeCtx, cancel := context.WithTimeout(ctx, totalDuration)
		defer cancel()

		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		// Continuously trigger events until the timeout.
		for {
			select {
			case <-ticker.C:
				// Each tick triggers multiple concurrent calls.
				for range numRoutines {
					go func() {
						assert.NoError(t, th.Execute(ctx, callback))
					}()
				}
			case <-timeCtx.Done():
				// Allow any trailing call to execute.
				time.Sleep(throttleWindow)
				// Expect one execution per window plus one trailing call.
				assert.Equal(t, int32(numWindows+1), atomic.LoadInt32(&callCount))
				return
			}
		}
	})
}

// TestCallbackErrorPropagation checks that errors returned by the callback
// are immediately propagated back to the caller.
func TestCallbackErrorPropagation(t *testing.T) {
	ctx := context.Background()
	const throttleWindow = 10 * time.Millisecond
	expectedErr := errors.New("callback error")

	t.Run("Immediate callback error is propagated", func(t *testing.T) {
		th := New(throttleWindow)
		callback := func() error {
			return expectedErr
		}
		err := th.Execute(ctx, callback)
		assert.ErrorIs(t, err, expectedErr)
	})
}

// TestContextCancellation verifies the throttler's behavior when the context
// expires (deadline exceeded) or is canceled.
func TestContextCancellation(t *testing.T) {
	const throttleWindow = 10 * time.Millisecond

	// In this test the context deadline is shorter than the throttle window.
	// The trailing call should fail with a deadline exceeded error.
	t.Run("Trailing call fails due to context deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), throttleWindow/2)
		defer cancel()
		th := New(throttleWindow)
		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		// The first call executes immediately.
		assert.NoError(t, th.Execute(ctx, callback))
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
		// The second call is delayed and should eventually time out.
		err := th.Execute(ctx, callback)
		assert.ErrorAs(t, err, &context.DeadlineExceeded)
		// Ensure the callback was not executed a second time.
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
	})

	// This test verifies that when the context is canceled,
	// any debouncing (trailing) call does not execute.
	t.Run("Trailing call fails due to context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		th := New(throttleWindow)
		var callCount int32
		callback := func() error {
			atomic.AddInt32(&callCount, 1)
			return nil
		}

		// First call executes immediately.
		assert.NoError(t, th.Execute(ctx, callback))
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
		// Launch a trailing call that will be affected by cancellation.
		var wg sync.WaitGroup
		wg.Add(1)

		go func() {
			err := th.Execute(ctx, callback)
			assert.ErrorAs(t, err, &context.Canceled)
			// Verify that the trailing call was not executed.
			assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
			wg.Done()
		}()
		// Cancel the context to cancel any debouncing trailing call.
		cancel()
		wg.Wait()
	})
}
