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
	mu sync.Mutex
	// waiters is the number of waiters waiting to acquire the lock
	// this is int32 instead of uint32 so we can add `-1` in `dec()`
	waiters int32
}

// inc increments the number of waiters waiting for the lock
func (l *lockCtr) inc() {
	atomic.AddInt32(&l.waiters, 1)
}

// dec decrements the number of waiters waiting on the lock
func (l *lockCtr) dec() {
	atomic.AddInt32(&l.waiters, -1)
}

// count gets the current number of waiters
func (l *lockCtr) count() int32 {
	return atomic.LoadInt32(&l.waiters)
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
	nameLock.inc()
	l.mu.Unlock()

	// Lock the nameLock outside the main mutex so we don't block other operations
	// once locked then we can decrement the number of waiters for this lock
	nameLock.Lock()
	nameLock.dec()
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
	nameLock.inc()
	l.mu.Unlock()

	// Lock the nameLock outside the main mutex so we don't block other operations
	// once locked then we can decrement the number of waiters for this lock
	succeeded := nameLock.TryLock()
	nameLock.dec()

	return succeeded
}

// Unlock unlocks the mutex with the given name
// If the given lock is not being waited on by any other callers, it is deleted
func (l *Locker) Unlock(name string) error {
	l.mu.Lock()
	nameLock, exists := l.locks[name]
	if !exists {
		l.mu.Unlock()
		return ErrNoSuchLock
	}

	if nameLock.count() == 0 {
		delete(l.locks, name)
	}
	nameLock.Unlock()

	l.mu.Unlock()
	return nil
}
