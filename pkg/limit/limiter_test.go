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

// TestSynchronousExecution verifies the behavior of synchronous calls to the throttler.
func TestSynchronousExecution(t *testing.T) {
	const (
		expireBatchSize = 100
		expireInterval  = 10 * time.Millisecond
		throttleWindow  = 100 * time.Millisecond
		debouncingTime  = 100 * time.Millisecond
		waitingTime     = expireInterval + throttleWindow + debouncingTime
		numExecute      = 1000
	)

	t.Run("Single Call", func(t *testing.T) {
		lim := limit.NewLimiter[string](expireBatchSize, expireInterval, throttleWindow, debouncingTime)
		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		if lim.Allow("key", callback) {
			callback()
		}
		time.Sleep(waitingTime)
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, int32(1), atomic.LoadInt32(&callCount))
	})

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

		for i := range numExecute {
			i := i
			go func() {
				if lim.Allow(i, callback) {
					callback()
				}
			}()
		}

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
