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

// Package limit provides event timing control components.
package limit

import (
	"container/list"
	"sync"
	"time"
)

// Limiter is a cache that ensures the most recently accessed keys have a TTL
// beyond which keys are forcibly expired.
type Limiter[K comparable] struct {
	mu        sync.Mutex
	closeChan chan struct{}

	expireInterval time.Duration
	bucketTTL      time.Duration
	limitWindow    time.Duration

	evictionList *list.List
	limiters     map[K]*list.Element
}

// New creates a new Limiter with the specified expiration interval, TTL, and rate limit window.
func New[K comparable](expireInterval, ttl, limitWindow time.Duration) *Limiter[K] {
	lim := &Limiter[K]{
		closeChan:      make(chan struct{}),
		expireInterval: expireInterval,
		bucketTTL:      ttl,
		limitWindow:    limitWindow,
		evictionList:   list.New(),
		limiters:       make(map[K]*list.Element),
	}

	// Start the expiration process in a separate goroutine.
	go lim.processLoop()
	return lim
}

// limitEntry holds the rate limiter and its expiration time for a given key.
type limitEntry[K comparable] struct {
	key                K
	bucket             Bucket
	expireTime         time.Time
	debouncingCallback func()
}

// Allow checks if an event for the given key is allowed according to the rate limiter.
// If the event is not allowed, the provided callback is stored for debouncing and later execution upon eviction.
func (l *Limiter[K]) Allow(key K, callback func()) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	if elem, exists := l.limiters[key]; exists {
		entry := elem.Value.(*limitEntry[K])
		allowed := entry.bucket.Allow(now)
		// If allowed, clear any pending debouncing callback; if not, store the new callback.
		if allowed {
			entry.debouncingCallback = nil
		} else {
			entry.debouncingCallback = callback
		}

		// Update recency and extend TTL.
		l.evictionList.MoveToFront(elem)
		entry.expireTime = now.Add(l.bucketTTL)
		return allowed
	}

	// Create a new rate bucket for the key.
	bucket := NewBucket(now, l.limitWindow)

	entry := &limitEntry[K]{
		key:        key,
		bucket:     bucket,
		expireTime: now.Add(l.bucketTTL),
	}
	elem := l.evictionList.PushFront(entry)
	l.limiters[key] = elem
	return true
}

// processLoop periodically calls expire to remove expired entries.
func (l *Limiter[K]) processLoop() {
	ticker := time.NewTicker(l.expireInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			l.expire()
		case <-l.closeChan:
			return
		}
	}
}

// expire removes entries from the eviction list that have passed their expiration time
// and triggers their debouncing callbacks if present.
func (l *Limiter[K]) expire() {
	now := time.Now()
	expiredEntries := make([]*limitEntry[K], 0, 100)

	l.mu.Lock()
	// Remove all expired entries from the back of the eviction list.
	for {
		elem := l.evictionList.Back()
		if elem == nil {
			break
		}

		entry := elem.Value.(*limitEntry[K])
		if now.Before(entry.expireTime) {
			break
		}

		expiredEntries = append(expiredEntries, entry)
		l.evictionList.Remove(elem)
		delete(l.limiters, entry.key)
	}
	l.mu.Unlock()

	for _, entry := range expiredEntries {
		if entry.debouncingCallback != nil {
			entry.debouncingCallback()
		}
	}
}

// Close stops the process loop and cleans up resources.
func (l *Limiter[K]) Close() {
	close(l.closeChan)
}
