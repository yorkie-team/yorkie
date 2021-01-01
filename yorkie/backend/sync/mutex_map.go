/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package sync

import (
	"errors"
	"sync"
)

var (
	errAlreadyUnlockedKey = errors.New("already unlocked key")
)

// MutexMap is a memory MutexMap.
// TODO: Temporary mutex map.
//  - We will need to replace this with distributed lock later.
//  - https://github.com/minio/dsync#other-techniques
type MutexMap struct {
	mutexMap *sync.Map
}

// NewMutexMap creates an instance of MutexMap.
func NewMutexMap() *MutexMap {
	return &MutexMap{
		mutexMap: &sync.Map{},
	}
}

// Unlock unlocks the given key's mutex.
func (m *MutexMap) Unlock(key string) error {
	value, ok := m.mutexMap.Load(key)
	if !ok {
		return errAlreadyUnlockedKey
	}

	mu := value.(*sync.Mutex)
	m.mutexMap.Delete(key)
	mu.Unlock()

	return nil
}

// Lock locks the given key's mutex.
func (m *MutexMap) Lock(key string) error {
	mu := sync.Mutex{}
	value, _ := m.mutexMap.LoadOrStore(key, &mu)
	loadedMu := value.(*sync.Mutex)
	loadedMu.Lock()
	if loadedMu != &mu {
		loadedMu.Unlock()
		return m.Lock(key)
	}

	return nil
}
