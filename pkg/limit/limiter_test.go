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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/pkg/limit"
)

// TestSynchronousExecute verifies the behavior of synchronous calls to the throttler.
func TestSynchronousExecute(t *testing.T) {
	const (
		expireBatch    = 100
		expireInterval = 10 * time.Millisecond
		throttleWindow = 100 * time.Millisecond
		debouncingTime = 100 * time.Millisecond
		waitingTime    = expireInterval + throttleWindow + debouncingTime
		numExecute     = 10000
	)

	t.Run("Single call test", func(t *testing.T) {
		lim := limit.New[string](expireBatch, expireInterval, throttleWindow, debouncingTime)
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

	t.Run("Many synchronous call with trailing debounced event", func(t *testing.T) {
		lim := limit.New[string](expireBatch, expireInterval, throttleWindow, debouncingTime)
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

// TestConcurrentExecute verifies the throttler behavior under concurrent execution scenarios.
func TestConcurrentExecute(t *testing.T) {
	const (
		expireBatch    = 10000
		expireInterval = 10 * time.Millisecond
		throttleWindow = 100 * time.Millisecond
		debouncingTime = 100 * time.Millisecond
		waitingTime    = expireInterval + throttleWindow + debouncingTime
		numExecute     = 10000
	)

	t.Run("Concurrent calls result in one immediate and one trailing execution", func(t *testing.T) {
		lim := limit.New[string](expireBatch, expireInterval, throttleWindow, debouncingTime)

		var callCount int32
		callback := func() {
			atomic.AddInt32(&callCount, 1)
		}

		for range numExecute {
			go func() {
				if lim.Allow("key", callback) {
					callback()
				}
			}()
		}

		time.Sleep(waitingTime)
		// Expect one immediate and one trailing callback execution.
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
		lim.Close()
		assert.Equal(t, int32(2), atomic.LoadInt32(&callCount))
	})

	t.Run("Concurrent calls with different keys", func(t *testing.T) {
		lim := limit.New[int](expireBatch, expireInterval, throttleWindow, debouncingTime)
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

	// This test simulates a continuous stream of events.
	// It triggers multiple concurrent calls at a regular interval and checks that throttling
	// limits the total number of callback invocations to one per window plus one trailing call.
	t.Run("Throttling over continuous event stream", func(t *testing.T) {
		const (
			numWindows     = 10                                             // Number of throttle windows.
			eventPerWindow = 1000                                           // Number of events triggered within each window.
			numRoutines    = 100                                            // Number of concurrent calls per tick.
			totalDuration  = throttleWindow * time.Duration(numWindows)     // Total simulation duration.
			eventInterval  = throttleWindow / time.Duration(eventPerWindow) // Interval between events.
		)

		lim := limit.New[string](expireBatch, expireInterval, throttleWindow, debouncingTime)

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
				for range numRoutines {
					go func() {
						if lim.Allow("key", callback) {
							callback()
						}
					}()
				}
			case <-timeCtx.Done():
				// After the event stream stops, allow time for any trailing callback to execute.
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

// TestExpireBatch verifies that the expiration loop processes expired entries in batches.
func TestExpireBatch(t *testing.T) {
	const (
		expireBatch    = 10
		expireInterval = 10 * time.Millisecond
		throttleWindow = 50 * time.Millisecond
		debouncingTime = 50 * time.Millisecond
		waitingTime    = expireInterval + throttleWindow + debouncingTime
		totalKeys      = expireBatch * 3 // create more keys than one batch
	)

	lim := limit.New[string](expireBatch, expireInterval, throttleWindow, debouncingTime)
	var callCount int32
	callback := func() {
		atomic.AddInt32(&callCount, 1)
	}

	// Create totalKeys entries, each with one immediate call and one trailing (debounced) callback.
	// For each key, the first call is allowed (and calls callback immediately),
	// while the second call queues the callback.
	for i := 0; i < totalKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		// First call: immediate execution.
		if lim.Allow(key, callback) {
			callback()
		}
		// Second call: queues the debounced callback.
		lim.Allow(key, callback)
	}

	// Wait for all debounced callbacks to run. The expirationLoop will process them in batches.
	time.Sleep(waitingTime)

	// For each key, we expect one immediate call and one debounced call.
	expected := int32(totalKeys * 2)
	assert.Equal(t, expected, atomic.LoadInt32(&callCount))
}
