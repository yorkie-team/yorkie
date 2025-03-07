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

// Package limit provides rate-limiting functionality with debouncing support.
package limit

import (
	"container/list"
	"sync"
	"time"
)

// Limiter provides rate limiting functionality with a debouncing callback.
// It maintains a single token bucket.
type Limiter[K comparable] struct {
	mu        sync.Mutex
	wg        sync.WaitGroup
	closeChan chan struct{}

	expireInterval time.Duration
	throttleWindow time.Duration
	debouncingTime time.Duration
	expireBatch    int

	// evictionList holds the limiter entries in order of recency.
	evictionList *list.List
	// entries maps keys to their corresponding list element for quick lookup.
	entries map[K]*list.Element
}

// New creates and returns a new Limiter instance.
// Parameters:
//
//	expireInterval: How often to check for expired entries.
//	throttleWindow: The time window for rate limiting.
//	debouncingTime: The time-to-live for each rate bucket entry.
func New[K comparable](expireNum int, expire, throttle, debouncing time.Duration) *Limiter[K] {
	lim := &Limiter[K]{
		closeChan:      make(chan struct{}),
		expireInterval: expire,
		throttleWindow: throttle,
		debouncingTime: debouncing,
		expireBatch:    expireNum,
		evictionList:   list.New(),
		entries:        make(map[K]*list.Element),
	}

	// Start the background expiration process.
	go lim.expirationLoop()
	return lim
}

// limiterEntry represents an entry in the Limiter for a specific key.
type limiterEntry[K comparable] struct {
	key                K
	bucket             Bucket
	expireTime         time.Time
	debouncingCallback func()
}

// Allow checks if an event is allowed for the given key based on the rate bucket.
// If allowed, it clears any pending debouncing callback; otherwise, it stores the provided callback.
// It returns true if the event is allowed immediately.
func (l *Limiter[K]) Allow(key K, callback func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if elem, exists := l.entries[key]; exists {
		entry := elem.Value.(*limiterEntry[K])
		allowed := entry.bucket.Allow(now)
		if allowed {
			entry.debouncingCallback = nil
		} else {
			entry.debouncingCallback = callback
		}
		// Update recency and extend TTL.
		l.evictionList.MoveToFront(elem)
		entry.expireTime = now.Add(l.throttleWindow + l.debouncingTime)
		return allowed
	}

	// Create a new rate bucket for a new key.
	bucket := NewBucket(now, l.throttleWindow)
	entry := &limiterEntry[K]{
		key:        key,
		bucket:     bucket,
		expireTime: now.Add(l.throttleWindow + l.debouncingTime),
	}
	elem := l.evictionList.PushFront(entry)
	l.entries[key] = elem
	return true
}

// expirationLoop runs in a separate goroutine to periodically remove expired entries.
func (l *Limiter[K]) expirationLoop() {
	ticker := time.NewTicker(l.expireInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			expiredEntries := l.collectExpired()
			l.runDebounce(expiredEntries)
		case <-l.closeChan:
			return
		}
	}
}

// collectExpired gathers expired entries and removes them from the limiter.
func (l *Limiter[K]) collectExpired() []*limiterEntry[K] {
	now := time.Now()
	expiredEntries := make([]*limiterEntry[K], 0, l.expireBatch)

	l.mu.Lock()
	defer l.mu.Unlock()

	for range l.expireBatch {
		elem := l.evictionList.Back()
		if elem == nil {
			break
		}

		entry := elem.Value.(*limiterEntry[K])
		if now.Before(entry.expireTime) {
			break
		}

		expiredEntries = append(expiredEntries, entry)
		l.evictionList.Remove(elem)
		delete(l.entries, entry.key)
	}

	return expiredEntries
}

// runDebounce runs the debouncing callbacks for expired entries asynchronously.
func (l *Limiter[K]) runDebounce(entries []*limiterEntry[K]) {
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		for _, entry := range entries {
			if entry.debouncingCallback != nil {
				entry.debouncingCallback()
			}
		}
	}()
}

// Close terminates the expiration loop and cleans up resources.
func (l *Limiter[K]) Close() {
	close(l.closeChan)
	for expiredEntries := l.collectExpired(); len(expiredEntries) > 0; {
		l.runDebounce(expiredEntries)
	}

	l.wg.Wait()
}
