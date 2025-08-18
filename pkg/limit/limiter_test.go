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
	"fmt"
	"sync"
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
		executeTime     = 5 * time.Millisecond
		waitingTime     = expireInterval + throttleWindow + debouncingTime + executeTime
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
		executeTime     = 5 * time.Millisecond
		waitingTime     = expireInterval + throttleWindow + debouncingTime + executeTime
		numExecute      = 1000
	)

	t.Run("Multiple Synchronous Calls with Trailing Debounce", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := occurs{}
		callback := func() { o.add(1) }

		for range numExecute {
			if lim.Allow("key", callback) {
				callback()
			}
		}
		assert.Equal(t, 1, o.len())

		time.Sleep(waitingTime)
		assert.Equal(t, 2, o.len())
		lim.Close()
		assert.Equal(t, 2, o.len())
	})

	t.Run("Concurrent Calls: Single Immediate and Trailing Execution", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := occurs{}
		callback := func() { o.add(1) }

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
		assert.Equal(t, 1, o.len())

		time.Sleep(waitingTime)
		assert.Equal(t, 2, o.len())
		lim.Close()
		assert.Equal(t, 2, o.len())
	})

	t.Run("Concurrent Calls with Different Keys", func(t *testing.T) {
		lim := limit.NewLimiter[int](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := occurs{
			array: make([]int, 0, numExecute*2),
		}
		callback := func() { o.add(1) }

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
		assert.Equal(t, numExecute, o.len())
		lim.Close()
		assert.Equal(t, numExecute, o.len())
	})

	t.Run("Continuous Event Stream Throttling", func(t *testing.T) {
		const (
			numWindows = 3 // Number of throttle windows.
		)

		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)

		o := occurs{
			array: make([]int, 0, numWindows+1),
		}

		// Continuously trigger events until the simulation ends.
		for i := range numWindows {
			callback := func() { o.add(i) }
			for range numExecute {
				if lim.Allow("key", callback) {
					callback()
				}
			}
			time.Sleep(throttleWindow)
		}
		assert.Equal(t, numWindows, o.len())
		time.Sleep(waitingTime)
		assert.Equal(t, numWindows+1, o.len())

		for i := range numWindows {
			assert.Equal(t, i, o.get(i))
		}
	})
}

// TestBatchExpiration verifies that the expiration loop processes expired entries in batches.
func TestBatchExpiration(t *testing.T) {
	const (
		expireInterval = 100 * time.Millisecond
		throttleWindow = 50 * time.Millisecond
		debouncingTime = 50 * time.Millisecond

		expireBatchSize = 10
		batchNum        = 3

		totalKeys = expireBatchSize * batchNum // create more keys than one batch
	)

	// Enhanced callback with completion tracking
	createCallbackWithTracking := func(o *occurs, completionChan chan struct{}) func() {
		return func() {
			o.add(1)
			select {
			case completionChan <- struct{}{}:
			default: // non-blocking send
			}
		}
	}

	t.Run("Process Expire Batch", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := occurs{
			array: make([]int, 0, totalKeys*2),
		}

		// Channel to track callback completions
		completionChan := make(chan struct{}, totalKeys*2)
		callback := createCallbackWithTracking(&o, completionChan)

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

		assert.Equal(t, totalKeys, o.len())

		// Wait for immediate callbacks to be processed
		for i := 0; i < totalKeys; i++ {
			select {
			case <-completionChan:
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Timeout waiting for immediate callback %d", i)
			}
		}

		// Wait for each batch with both count checking and completion tracking
		for i := range batchNum {
			expectedCount := totalKeys + expireBatchSize*(i+1)

			// First, wait for the callbacks to actually execute
			for j := range expireBatchSize {
				select {
				case <-completionChan:
				case <-time.After(expireInterval * 2):
					t.Fatalf("Timeout waiting for batch %d callback %d", i+1, j+1)
				}
			}

			// Then verify the count (should be immediate now)
			assert.Equal(t, expectedCount, o.len(), "Batch %d should be complete", i+1)
		}

		assert.Equal(t, totalKeys+expireBatchSize*batchNum, o.len())
		lim.Close()
		assert.Equal(t, totalKeys+expireBatchSize*batchNum, o.len())
	})

	t.Run("Force Close Expired", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		o := occurs{
			array: make([]int, 0, totalKeys*2),
		}
		callback := func() { o.add(1) }

		for i := range totalKeys {
			key := fmt.Sprintf("key-%d", i)
			// Immediate execution.
			if lim.Allow(key, callback) {
				callback()
			}
			// Queue the debounced callback.
			lim.Allow(key, callback)
		}

		assert.Equal(t, totalKeys, o.len())

		done := make(chan struct{})
		go func() {
			lim.Close()
			done <- struct{}{}
		}()
		select {
		case <-done:
			assert.Equal(t, totalKeys+expireBatchSize*batchNum, o.len())
		case <-time.After(expireInterval):
			assert.Equal(t, totalKeys+expireBatchSize*batchNum, o.len())
			assert.Fail(t, "close timeout")
		}
	})
}
