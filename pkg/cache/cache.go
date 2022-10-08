/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
 *
 * reference from the Kubernetes repository:
 * https://github.com/kubernetes/kubernetes/blob/master/staging/src/k8s.io/apimachinery/pkg/util/cache/lruexpirecache.go
 */

// Package cache provides a cache implementation.
package cache

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

var (
	// ErrInvalidMaxSize is returned when the given max size is not positive.
	ErrInvalidMaxSize = errors.New("max size must be > 0")
)

// LRUExpireCache is a cache that ensures the mostly recently accessed keys are returned with
// a ttl beyond which keys are forcibly expired.
type LRUExpireCache[K comparable, V any] struct {
	lock sync.Mutex

	maxSize      int
	evictionList list.List
	entries      map[K]*list.Element
}

// NewLRUExpireCache creates an expiring cache with the given size
func NewLRUExpireCache[K comparable, V any](maxSize int) (*LRUExpireCache[K, V], error) {
	if maxSize <= 0 {
		return nil, ErrInvalidMaxSize
	}

	return &LRUExpireCache[K, V]{
		maxSize: maxSize,
		entries: map[K]*list.Element{},
	}, nil
}

type cacheEntry[K comparable, V any] struct {
	key        K
	value      V
	expireTime time.Time
}

// Add adds the value to the cache at key with the specified maximum duration.
func (c *LRUExpireCache[K, V]) Add(
	key K,
	value V,
	ttl time.Duration,
) {
	c.lock.Lock()
	defer c.lock.Unlock()

	oldElement, ok := c.entries[key]
	if ok {
		c.evictionList.MoveToFront(oldElement)
		oldElement.Value.(*cacheEntry[K, V]).value = value
		oldElement.Value.(*cacheEntry[K, V]).expireTime = time.Now().Add(ttl)
		return
	}

	if c.evictionList.Len() >= c.maxSize {
		toEvict := c.evictionList.Back()
		c.evictionList.Remove(toEvict)
		delete(c.entries, toEvict.Value.(*cacheEntry[K, V]).key)
	}

	element := c.evictionList.PushFront(&cacheEntry[K, V]{
		key:        key,
		value:      value,
		expireTime: time.Now().Add(ttl),
	})
	c.entries[key] = element
}

// Get returns the value at the specified key from the cache if it exists and is not
// expired, or returns false.
func (c *LRUExpireCache[K, V]) Get(key K) (V, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	var nilV V

	element, ok := c.entries[key]
	if !ok {
		return nilV, false
	}

	if time.Now().After(element.Value.(*cacheEntry[K, V]).expireTime) {
		c.evictionList.Remove(element)
		delete(c.entries, key)
		return nilV, false
	}

	c.evictionList.MoveToFront(element)

	return element.Value.(*cacheEntry[K, V]).value, true
}
