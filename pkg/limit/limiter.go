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

// Limiter provides a combined throttling and debouncing mechanism
// that ensures eventual consistency.
type Limiter struct {
	lim        *rate.Limiter
	debouncing int32 // 0 means false, 1 means true
}

// New creates a new instance with the specified throttle intervals.
func New(window time.Duration) *Limiter {
	dt := &Limiter{
		lim:        rate.NewLimiter(rate.Every(window), 1),
		debouncing: 0,
	}
	return dt
}

// Execute attempts to run the provided callback function immediately if the rate limiter allows it.
// If the rate limiter does not allow immediate execution, this function blocks until the next token
// is available and then runs the callback. If there is already a debouncing callback, Execute returns
// immediately. This mechanism ensures that the final callback is executed after the final event,
// providing eventual consistency.
func (l *Limiter) Execute(ctx context.Context, callback func() error) error {
	if l.lim.Allow() {
		return callback()
	}

	if !atomic.CompareAndSwapInt32(&l.debouncing, 0, 1) {
		return nil
	}

	if err := l.lim.Wait(ctx); err != nil {
		return fmt.Errorf("wait for limiter: %w", err)
	}

	if err := callback(); err != nil {
		atomic.StoreInt32(&l.debouncing, 0)
		return fmt.Errorf("callback: %w", err)
	}
	atomic.StoreInt32(&l.debouncing, 0)

	return nil
}
