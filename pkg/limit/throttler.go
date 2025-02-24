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
	"fmt"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// Throttler provides a combined throttling and debouncing mechanism
// that ensures eventual consistency. It calls the callback immediately
// if allowed by the rate limiter; otherwise, it schedules a trailing callback.
type Throttler struct {
	lim     *rate.Limiter
	pending int32 // 0 means false, 1 means true
}

// New creates a new instance with the specified throttle intervals.
func New(window time.Duration) *Throttler {
	dt := &Throttler{
		lim:     rate.NewLimiter(rate.Every(window), 1),
		pending: 0,
	}
	return dt
}

// Execute attempts to run the provided callback function immediately if the rate limiter allows it.
// If the rate limiter does not allow immediate execution, this function blocks until the next token
// is available and then runs the callback. If there is already a pending callback, Execute returns
// immediately. This mechanism ensures that the final callback is executed after the final event,
// providing eventual consistency.
func (t *Throttler) Execute(ctx context.Context, callback func() error) error {
	if t.lim.Allow() {
		return callback()
	}

	if !atomic.CompareAndSwapInt32(&t.pending, 0, 1) {
		return nil
	}

	if err := t.lim.Wait(ctx); err != nil {
		return fmt.Errorf("wait for limiter: %w", err)
	}

	atomic.StoreInt32(&t.pending, 0)
	return callback()
}

// ExecuteOrSchedule is the asynchronous counterpart to Execute. It runs the provided callback
// immediately if the rate limiter allows it. Otherwise, it schedules the callback to run
// once the next token becomes available and returns immediately. If there is already a pending
// callback, this function returns without scheduling another one.
func (t *Throttler) ExecuteOrSchedule(callback func()) {
	if t.lim.Allow() {
		callback()
		return
	}

	if !atomic.CompareAndSwapInt32(&t.pending, 0, 1) {
		return
	}
	delay := t.lim.Reserve().Delay()
	time.AfterFunc(delay, func() {
		atomic.StoreInt32(&t.pending, 0)
		callback()
	})
}
