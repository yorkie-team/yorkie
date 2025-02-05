/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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
 * This file was written with reference to moby/locker.
 *   https://github.com/moby/locker
 */

/*
Package locker provides a mechanism for creating finer-grained locking to help
free up more global locks to handle other tasks.

The implementation looks close to a sync.Mutex, however the user must provide a
reference to use to refer to the underlying lock when locking and unlocking,
and unlock may generate an error.

If a lock with a given name does not exist when `Lock` is called, one is
created.
Lock references are automatically cleaned up on `Unlock` if nothing else is
waiting for the lock.
*/
package locker

import (
	"errors"
	"sync"
	"sync/atomic"
)

// ErrNoSuchLock is returned when the requested lock does not exist
var ErrNoSuchLock = errors.New("no such lock")

// Locker provides a locking mechanism based on the passed in reference name
type Locker struct {
	mu    sync.Mutex
	locks map[string]*lockCtr
}

// lockCtr is used by Locker to represent a lock with a given name.
type lockCtr struct {
	mu sync.RWMutex
	// waiters is the number of waiters waiting to acquire the lock
	// this is int32 instead of uint32 so we can add `-1` in `decWaiters()`
	waiters int32
	// readers is the number of readers currently holding RLock.
	readers int32
	// writer is 1 if currently holding Lock, otherwise 0.
	writer int32
}

// incWaiters increments the number of waiters waiting for the lock
func (l *lockCtr) incWaiters() {
	atomic.AddInt32(&l.waiters, 1)
}

// decWaiters decrements the number of waiters waiting on the lock
func (l *lockCtr) decWaiters() {
	atomic.AddInt32(&l.waiters, -1)
}

func (l *lockCtr) incReaders() {
	atomic.AddInt32(&l.readers, 1)
}

func (l *lockCtr) decReaders() {
	atomic.AddInt32(&l.readers, -1)
}

func (l *lockCtr) setWriter(val int32) {
	atomic.StoreInt32(&l.writer, val)
}

// count gets the current number of waiters
func (l *lockCtr) count() int32 {
	return atomic.LoadInt32(&l.waiters)
}

func (l *lockCtr) canDelete() bool {
	return atomic.LoadInt32(&l.waiters) == 0 &&
		atomic.LoadInt32(&l.readers) == 0 &&
		atomic.LoadInt32(&l.writer) == 0
}

// Lock locks the mutex
func (l *lockCtr) Lock() {
	l.mu.Lock()
}

// TryLock tries to lock the mutex.
func (l *lockCtr) TryLock() bool {
	return l.mu.TryLock()
}

// Unlock unlocks the mutex
func (l *lockCtr) Unlock() {
	l.mu.Unlock()
}

// RLock locks the mutex
func (l *lockCtr) RLock() {
	l.mu.RLock()
}

// TryRLock tries to lock the mutex.
func (l *lockCtr) TryRLock() bool {
	return l.mu.TryRLock()
}

// RUnlock unlocks the mutex
func (l *lockCtr) RUnlock() {
	l.mu.RUnlock()
}

// New creates a new Locker
func New() *Locker {
	return &Locker{
		locks: make(map[string]*lockCtr),
	}
}

// Lock locks a mutex with the given name. If it doesn't exist, one is created
func (l *Locker) Lock(name string) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*lockCtr)
	}

	nameLock, exists := l.locks[name]
	if !exists {
		nameLock = &lockCtr{}
		l.locks[name] = nameLock
	}

	// increment the nameLock waiters while inside the main mutex
	// this makes sure that the lock isn't deleted if `Lock` and `Unlock` are called concurrently
	nameLock.incWaiters()
	l.mu.Unlock()

	// Lock the nameLock outside the main mutex so we don't block other operations
	// once locked then we can decrement the number of waiters for this lock
	nameLock.Lock()

	nameLock.decWaiters()
	nameLock.setWriter(1)
}

// TryLock locks a mutex with the given name. If it doesn't exist, one is created.
func (l *Locker) TryLock(name string) bool {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*lockCtr)
	}

	nameLock, exists := l.locks[name]
	if !exists {
		nameLock = &lockCtr{}
		l.locks[name] = nameLock
	}

	// increment the nameLock waiters while inside the main mutex
	// this makes sure that the lock isn't deleted if `Lock` and `Unlock` are called concurrently
	nameLock.incWaiters()
	l.mu.Unlock()

	// Lock the nameLock outside the main mutex so we don't block other operations
	// once locked then we can decrement the number of waiters for this lock
	succeeded := nameLock.TryLock()
	nameLock.decWaiters()
	nameLock.setWriter(1)

	return succeeded
}

// Unlock unlocks the mutex with the given name
// If the given lock is not being waited on by any other callers, it is deleted
func (l *Locker) Unlock(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	nameLock, exists := l.locks[name]
	if !exists {
		return ErrNoSuchLock
	}

	nameLock.Unlock()
	nameLock.setWriter(0)

	if nameLock.canDelete() {
		delete(l.locks, name)
	}

	return nil
}

// RLock acquires a read lock for the given name.
// If there is no lock for that name, a new one is created.
func (l *Locker) RLock(name string) {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*lockCtr)
	}

	nameLock, exists := l.locks[name]
	if !exists {
		nameLock = &lockCtr{}
		l.locks[name] = nameLock
	}

	// 01. Increase waiters inside the global lock
	nameLock.incWaiters()
	l.mu.Unlock()

	// 02. Acquire RLock
	nameLock.RLock()

	// 03. Decrease waiters and increase readers
	nameLock.decWaiters()
	nameLock.incReaders()
}

// TryRLock attempts to acquire a read lock for the given name.
// Returns true if success, false if the lock is currently held by a writer.
func (l *Locker) TryRLock(name string) bool {
	l.mu.Lock()
	if l.locks == nil {
		l.locks = make(map[string]*lockCtr)
	}

	nameLock, exists := l.locks[name]
	if !exists {
		nameLock = &lockCtr{}
		l.locks[name] = nameLock
	}

	// increment the nameLock waiters while inside the main mutex
	// this makes sure that the lock isn't deleted if `Lock` and `Unlock` are called concurrently
	nameLock.incWaiters()
	l.mu.Unlock()

	// Lock the nameLock outside the main mutex so we don't block other operations
	// once locked then we can decrement the number of waiters for this lock
	succeeded := nameLock.TryRLock()

	nameLock.decWaiters()
	nameLock.incReaders()

	return succeeded
}

// RUnlock releases a read lock for the given name.
func (l *Locker) RUnlock(name string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	nameLock, exists := l.locks[name]
	if !exists {
		return ErrNoSuchLock
	}

	nameLock.RUnlock()
	nameLock.decReaders()

	if nameLock.canDelete() {
		delete(l.locks, name)
	}

	return nil
}
