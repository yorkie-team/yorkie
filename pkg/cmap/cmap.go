/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

// Package cmap provides a concurrent map.
package cmap

import (
	"sync"
)

// Map is a mutex-protected map.
type Map[K comparable, V any] struct {
	sync.RWMutex
	items map[K]V
}

// New creates a new Map.
func New[K comparable, V any]() *Map[K, V] {
	return &Map[K, V]{
		items: make(map[K]V),
	}
}

// Set sets a key-value pair.
func (m *Map[K, V]) Set(key K, value V) {
	m.Lock()
	defer m.Unlock()

	m.items[key] = value
}

// UpsertFunc is a function to insert or update a key-value pair.
type UpsertFunc[K comparable, V any] func(valueInMap V, exists bool) V

// Upsert inserts or updates a key-value pair.
func (m *Map[K, V]) Upsert(key K, upsertFunc UpsertFunc[K, V]) V {
	m.Lock()
	defer m.Unlock()

	v, exists := m.items[key]
	res := upsertFunc(v, exists)
	m.items[key] = res
	return res
}

// Get retrieves a value from the map.
func (m *Map[K, V]) Get(key K) (V, bool) {
	m.RLock()
	defer m.RUnlock()

	value, exists := m.items[key]
	return value, exists
}

// DeleteFunc is a function to delete a value from the map.
type DeleteFunc[K comparable, V any] func(value V, exists bool) bool

// Delete removes a value from the map.
func (m *Map[K, V]) Delete(key K, deleteFunc DeleteFunc[K, V]) bool {
	m.Lock()
	defer m.Unlock()

	value, exists := m.items[key]
	del := deleteFunc(value, exists)
	if del && exists {
		delete(m.items, key)
	}
	return del
}

// Has checks if a key exists in the map
func (m *Map[K, V]) Has(key K) bool {
	m.RLock()
	defer m.RUnlock()

	_, exists := m.items[key]
	return exists
}

// Len returns the number of items in the map
func (m *Map[K, V]) Len() int {
	m.RLock()
	defer m.RUnlock()

	return len(m.items)
}

// Keys returns a slice of all keys in the map
func (m *Map[K, V]) Keys() []K {
	m.RLock()
	defer m.RUnlock()

	keys := make([]K, 0, len(m.items))
	for k := range m.items {
		keys = append(keys, k)
	}
	return keys
}

// Values returns a slice of all values in the map
func (m *Map[K, V]) Values() []V {
	m.RLock()
	defer m.RUnlock()

	values := make([]V, 0, len(m.items))
	for _, v := range m.items {
		values = append(values, v)
	}
	return values
}
