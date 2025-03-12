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

// Package limit_test provides unit tests for event timing control components.
package limit_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/limit"
)

// occurs encapsulates a slice of integers with a mutex for concurrent access.
type occurs struct {
	array []int
	mu    sync.Mutex
}

// add appends a value to the array.
func (o *occurs) add(e int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.array = append(o.array, e)
}

// len returns the length of the array.
func (o *occurs) len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.array)
}

// get returns the element at the specified index.
func (o *occurs) get(index int) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.array[index]
}

// TestThrottlerBehavior verifies the behavior of synchronous calls to the throttler.
// You can refer to the visualization in https://github.com/yorkie-team/yorkie/pull/1166.
func TestThrottlerBehavior(t *testing.T) {
	const (
		expireBatchSize = 100
		expireInterval  = 10 * time.Millisecond
		throttleWindow  = 100 * time.Millisecond
		debouncingTime  = 100 * time.Millisecond
		waitingTime     = expireInterval + throttleWindow + debouncingTime
	)

	// Test case: "e1" -> e1 occurs
	t.Run("e1", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := &occurs{}

		e1 := func() { o.add(1) }
		if lim.Allow("key", e1) {
			e1()
		}
		// Immediately after execution, the callback should have been invoked.
		assert.Equal(t, 1, o.get(0))
		// After waiting, no additional invocation should occur.
		time.Sleep(waitingTime)
		assert.Equal(t, 1, o.len())
		lim.Close()
	})

	// Test case: "e1 e2" -> e1 then e2 occurs
	t.Run("e1 e2", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := &occurs{}

		e1 := func() { o.add(1) }
		if lim.Allow("key", e1) {
			e1()
		}
		// First callback is executed directly.
		assert.Equal(t, 1, o.get(0))

		e2 := func() { o.add(2) }
		if lim.Allow("key", e2) {
			e2()
		}

		// At this point, only the immediate callback should have occurred.
		assert.Equal(t, 1, o.len())
		time.Sleep(waitingTime)

		// After waiting, the deferred callback should be executed.
		assert.Equal(t, 2, o.len())
		assert.Equal(t, 2, o.get(1))
		lim.Close()
	})

	// Test case: "e1 e2 e3" -> e1 immediately and e3 deferred
	t.Run("e1 e2 e3", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := &occurs{}

		e1 := func() { o.add(1) }
		if lim.Allow("key", e1) {
			e1()
		}
		// First callback is executed immediately.
		assert.Equal(t, 1, o.get(0))

		e2 := func() { o.add(2) }
		if lim.Allow("key", e2) {
			e2()
		}
		e3 := func() { o.add(3) }
		if lim.Allow("key", e3) {
			e3()
		}

		// Only the immediate callback should have been executed so far.
		assert.Equal(t, 1, o.len())
		time.Sleep(waitingTime)

		// After waiting, the latest callback (e3) is executed.
		assert.Equal(t, 2, o.len())
		assert.Equal(t, 3, o.get(1))
		lim.Close()
	})

	// Test case: "/ e1 e2 e3 / e4" -> e1 immediately then e4 immediately when allowed
	t.Run("/ e1 e2 e3 / e4", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := &occurs{}

		e1 := func() { o.add(1) }
		if lim.Allow("key", e1) {
			e1()
		}
		// Immediate execution for the first callback.
		assert.Equal(t, 1, o.get(0))

		e2 := func() { o.add(2) }
		if lim.Allow("key", e2) {
			e2()
		}
		e3 := func() { o.add(3) }
		if lim.Allow("key", e3) {
			e3()
		}

		// Still, only the immediate callback should have been executed.
		assert.Equal(t, 1, o.len())

		// Wait for part of the throttle window; deferred callbacks are not yet flushed.
		time.Sleep(throttleWindow + debouncingTime/2)
		assert.Equal(t, 1, o.len())

		e4 := func() { o.add(4) }
		if lim.Allow("key", e4) {
			e4()
		}

		// The new callback should now be executed immediately.
		assert.Equal(t, 2, o.len())
		assert.Equal(t, 4, o.get(1))
		time.Sleep(waitingTime)
		// No further callbacks should be executed after waiting.
		assert.Equal(t, 2, o.len())
		lim.Close()
	})

	// Test case: "/ e1 e2 e3 / e4 e5" -> e1, then e4 immediately, then e5 deferred
	t.Run("/ e1 e2 e3 / e4 e5", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := &occurs{}

		// e1 occurs directly.
		e1 := func() { o.add(1) }
		if lim.Allow("key", e1) {
			e1()
		}
		assert.Equal(t, 1, o.get(0))

		// e2 is saved.
		e2 := func() { o.add(2) }
		if lim.Allow("key", e2) {
			e2()
		}
		// e3 replaces e2.
		e3 := func() { o.add(3) }
		if lim.Allow("key", e3) {
			e3()
		}
		assert.Equal(t, 1, o.len())
		time.Sleep(throttleWindow + debouncingTime/2)
		assert.Equal(t, 1, o.len())

		// Before flushing e3, e4 occurs so e3 is skipped.
		e4 := func() { o.add(4) }
		if lim.Allow("key", e4) {
			e4()
		}
		assert.Equal(t, 2, o.len())
		assert.Equal(t, 4, o.get(1))

		// e5 meets limit so it is saved.
		e5 := func() { o.add(5) }
		if lim.Allow("key", e5) {
			e5()
		}
		assert.Equal(t, 2, o.len())

		// And flushed when it expires.
		time.Sleep(waitingTime)
		assert.Equal(t, 3, o.len())
		assert.Equal(t, 5, o.get(2))
		lim.Close()
	})
}

// TestConcurrentExecution verifies the throttler behavior under concurrent execution scenarios.
func TestConcurrentExecution(t *testing.T) {
	const (
		expireBatchSize = 100
		expireInterval  = 10 * time.Millisecond
		throttleWindow  = 100 * time.Millisecond
		debouncingTime  = 100 * time.Millisecond
		waitingTime     = expireInterval + throttleWindow + debouncingTime
		numExecute      = 1000
	)

	t.Run("Multiple Synchronous Calls with Trailing Debounce", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		for range numExecute {
			if lim.Allow("key", callback) {
				callback()
			}
		}

		time.Sleep(waitingTime)
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
	})

	t.Run("Concurrent Calls: Single Immediate and Trailing Execution", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)

		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		wg := sync.WaitGroup{}
		for range numExecute {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if lim.Allow("key", callback) {
					callback()
				}
			}()
		}
		wg.Wait()

		time.Sleep(waitingTime)
		// Expect one immediate and one trailing callback execution.
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
	})

	t.Run("Concurrent Calls with Different Keys", func(t *testing.T) {
		lim := limit.NewLimiter[int](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		wg := sync.WaitGroup{}
		for i := range numExecute {
			i := i
			wg.Add(1)
			go func() {
				defer wg.Done()
				if lim.Allow(i, callback) {
					callback()
				}
			}()
		}
		wg.Wait()

		time.Sleep(waitingTime)
		// For different keys, every call is immediate so expect numExecute callbacks.
		assert.Equal(t, int32(numExecute), atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, int32(numExecute), atomic.LoadInt32(&callCount))
	})

	t.Run("Continuous Event Stream Throttling", func(t *testing.T) {
		const (
			numWindows     = 3                                              // Number of throttle windows.
			eventPerWindow = 100                                            // Number of events triggered within each window.
			numExecutes    = 100                                            // Number of concurrent calls per tick.
			totalDuration  = throttleWindow * time.Duration(numWindows)     // Total simulation duration.
			eventInterval  = throttleWindow / time.Duration(eventPerWindow) // Interval between events.
		)

		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)

		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		ticker := time.NewTicker(eventInterval)
		defer ticker.Stop()
		timeCtx, cancel := context.WithTimeout(context.Background(), totalDuration)
		defer cancel()

		// Continuously trigger events until the simulation ends.
		for {
			select {
			case <-ticker.C:
				// Each tick triggers multiple concurrent calls.
				for range numExecutes {
					if lim.Allow("key", callback) {
						callback()
					}
				}
			case <-timeCtx.Done():
				// Allow time for any trailing callback to execute.
				time.Sleep(waitingTime)
				// Expect one callback per throttle window plus one additional trailing call.
				assert.Equal(t, int32(numWindows+1), atomic.LoadInt32(&callCount))
				lim.Close()
				assert.Equal(t, int32(numWindows+1), atomic.LoadInt32(&callCount))
				return
			}
		}
	})
}

// TestBatchExpiration verifies that the expiration loop processes expired entries in batches.
func TestBatchExpiration(t *testing.T) {
	const (
		expireInterval = 100 * time.Millisecond
		throttleWindow = 50 * time.Millisecond
		debouncingTime = 50 * time.Millisecond

		expireBatchSize       = 10
		batchNum        int32 = 3

		totalKeys = expireBatchSize * batchNum // create more keys than one batch
	)

	t.Run("Process Expire Batch", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		// For each key: first call executes immediately, second call schedules a debounced callback.
		for i := range totalKeys {
			key := fmt.Sprintf("key-%d", i)
			// Immediate execution.
			if lim.Allow(key, callback) {
				callback()
			}
			// Queue the debounced callback.
			lim.Allow(key, callback)
		}

		assert.Equal(t, totalKeys, atomic.LoadInt32(&callCount))
		time.Sleep(expireInterval / 2)
		for i := range batchNum {
			assert.Equal(t, totalKeys+expireBatchSize*i, atomic.LoadInt32(&callCount))
			time.Sleep(expireInterval)
		}
		assert.Equal(t, totalKeys+expireBatchSize*batchNum, atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, totalKeys+expireBatchSize*batchNum, atomic.LoadInt32(&callCount))
	})

	t.Run("Force Close Expired", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		for i := range totalKeys {
			key := fmt.Sprintf("key-%d", i)
			// Immediate execution.
			if lim.Allow(key, callback) {
				callback()
			}
			// Queue the debounced callback.
			lim.Allow(key, callback)
		}

		assert.Equal(t, totalKeys, atomic.LoadInt32(&callCount))

		done := make(chan struct{})
		go func() {
			lim.Close()
			done <- struct{}{}
		}()
		select {
		case <-done:
			assert.Equal(t, totalKeys*2, atomic.LoadInt32(&callCount))
		case <-time.After(expireInterval):
			assert.Equal(t, totalKeys*2, atomic.LoadInt32(&callCount))
			assert.Fail(t, "close timeout")
		}
	})
}
