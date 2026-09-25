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

package limit

import "time"

// Bucket represents a single-token bucket that refills every specified time window.
type Bucket struct {
	window time.Duration // The interval at which the bucket refills.
	last   time.Time     // The last time a token was granted.
}

// NewBucket creates a new Bucket with the given initial time and refill window.
func NewBucket(now time.Time, window time.Duration) Bucket {
	return Bucket{
		window: window,
		last:   now,
	}
}

// Allow checks if a token can be granted at the given time.
// It returns true if the time has advanced past the refill window, otherwise false.
func (b *Bucket) Allow(now time.Time) bool {
	if now.Before(b.last.Add(b.window)) {
		return false
	}

	b.last = now
	return true
}
