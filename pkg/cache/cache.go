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
type LRUExpireCache struct {
	lock sync.Mutex

	maxSize      int
	evictionList list.List
	entries      map[string]*list.Element
}

// NewLRUExpireCache creates an expiring cache with the given size
func NewLRUExpireCache(maxSize int) (*LRUExpireCache, error) {
	if maxSize <= 0 {
		return nil, ErrInvalidMaxSize
	}

	return &LRUExpireCache{
		maxSize: maxSize,
		entries: map[string]*list.Element{},
	}, nil
}

type cacheEntry struct {
	key        string
	value      interface{}
	expireTime time.Time
}

// Add adds the value to the cache at key with the specified maximum duration.
func (c *LRUExpireCache) Add(
	key string,
	value interface{},
	ttl time.Duration,
) {
	c.lock.Lock()
	defer c.lock.Unlock()

	oldElement, ok := c.entries[key]
	if ok {
		c.evictionList.MoveToFront(oldElement)
		oldElement.Value.(*cacheEntry).value = value
		oldElement.Value.(*cacheEntry).expireTime = time.Now().Add(ttl)
		return
	}

	if c.evictionList.Len() >= c.maxSize {
		toEvict := c.evictionList.Back()
		c.evictionList.Remove(toEvict)
		delete(c.entries, toEvict.Value.(*cacheEntry).key)
	}

	element := c.evictionList.PushFront(&cacheEntry{
		key:        key,
		value:      value,
		expireTime: time.Now().Add(ttl),
	})
	c.entries[key] = element
}

// Get returns the value at the specified key from the cache if it exists and is not
// expired, or returns false.
func (c *LRUExpireCache) Get(key string) (interface{}, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	element, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	if time.Now().After(element.Value.(*cacheEntry).expireTime) {
		c.evictionList.Remove(element)
		delete(c.entries, key)
		return nil, false
	}

	c.evictionList.MoveToFront(element)

	return element.Value.(*cacheEntry).value, true
}
