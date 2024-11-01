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
	"fmt"
	"hash/fnv"
	"sync"
)

// numShards is the number of shards.
const numShards = 32

type shard[K comparable, V any] struct {
	sync.RWMutex
	items map[K]V
}

// Map is a concurrent map that is safe for multiple routines. It is optimized
// to reduce lock contention and improve performance.
type Map[K comparable, V any] struct {
	shards [numShards]shard[K, V]
}

// New creates a new Map.
func New[K comparable, V any]() *Map[K, V] {
	m := &Map[K, V]{}
	for i := 0; i < numShards; i++ {
		m.shards[i].items = make(map[K]V)
	}
	return m
}

// shardForKey returns the shard for the given key.
func (m *Map[K, V]) shardForKey(key K) *shard[K, V] {
	var idx uint32
	switch k := any(key).(type) {
	case string:
		hash := fnv.New32a()
		if _, err := hash.Write([]byte(k)); err != nil {
			panic(fmt.Sprintf("shard for key: %s", err))
		}
		idx = hash.Sum32()
	case int:
		idx = uint32(k)
	default:
		hash := fnv.New32a()
		if _, err := hash.Write([]byte(fmt.Sprintf("%v", key))); err != nil {
			panic(fmt.Sprintf("shard for key: %s", err))
		}
		idx = hash.Sum32()
	}

	return &m.shards[idx%numShards]
}

// shardOf returns the shard for the given index.
func (m *Map[K, V]) shardOf(idx int) *shard[K, V] {
	return &m.shards[idx%numShards]
}

// Set sets a key-value pair.
func (m *Map[K, V]) Set(key K, value V) {
	shard := m.shardForKey(key)

	shard.Lock()
	defer shard.Unlock()

	shard.items[key] = value
}

// UpsertFunc is a function to insert or update a key-value pair.
type UpsertFunc[K comparable, V any] func(value V, exists bool) V

// Upsert inserts or updates a key-value pair.
func (m *Map[K, V]) Upsert(key K, upsertFunc UpsertFunc[K, V]) V {
	shard := m.shardForKey(key)

	shard.Lock()
	defer shard.Unlock()

	v, exists := shard.items[key]
	res := upsertFunc(v, exists)
	shard.items[key] = res
	return res
}

// Get retrieves a value from the map.
func (m *Map[K, V]) Get(key K) (V, bool) {
	shard := m.shardForKey(key)

	shard.RLock()
	defer shard.RUnlock()

	value, exists := shard.items[key]
	return value, exists
}

// DeleteFunc is a function to delete a value from the map.
type DeleteFunc[K comparable, V any] func(value V, exists bool) bool

// Delete removes a value from the map.
func (m *Map[K, V]) Delete(key K, deleteFunc DeleteFunc[K, V]) bool {
	shard := m.shardForKey(key)

	shard.Lock()
	defer shard.Unlock()

	value, exists := shard.items[key]
	del := deleteFunc(value, exists)
	if del && exists {
		delete(shard.items, key)
	}

	return del
}

// Has checks if a key exists in the map
func (m *Map[K, V]) Has(key K) bool {
	shard := m.shardForKey(key)

	shard.RLock()
	defer shard.RUnlock()

	_, exists := shard.items[key]
	return exists
}

// Len returns the number of items in the map
func (m *Map[K, V]) Len() int {
	count := 0

	for i := 0; i < numShards; i++ {
		shard := &m.shards[i]

		shard.RLock()
		count += len(shard.items)
		shard.RUnlock()
	}

	return count
}

// Keys returns a slice of all keys in the map
func (m *Map[K, V]) Keys() []K {
	keys := make([]K, 0)

	for i := 0; i < numShards; i++ {
		shard := &m.shards[i]

		shard.RLock()
		for k := range shard.items {
			keys = append(keys, k)
		}
		shard.RUnlock()
	}
	return keys
}

// Values returns a slice of all values in the map
func (m *Map[K, V]) Values() []V {
	values := make([]V, 0)

	for i := 0; i < numShards; i++ {
		shard := &m.shards[i]

		shard.RLock()
		for _, v := range shard.items {
			values = append(values, v)
		}
		shard.RUnlock()
	}

	return values
}
