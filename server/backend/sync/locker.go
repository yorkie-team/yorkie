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
 */

// Package sync provides a locker implementation.
package sync

import (
	"errors"

	"github.com/yorkie-team/yorkie/pkg/locker"
)

// ErrAlreadyLocked is returned when the lock is already locked.
var ErrAlreadyLocked = errors.New("already locked")

// Key represents key of Locker.
type Key string

// NewKey creates a new instance of Key.
func NewKey(key string) Key {
	return Key(key)
}

// String returns a string representation of this Key.
func (k Key) String() string {
	return string(k)
}

// LockerManager manages Lockers.
type LockerManager struct {
	locks *locker.Locker
}

// New creates a new instance of LockerManager.
func New() *LockerManager {
	return &LockerManager{
		locks: locker.New(),
	}
}

// Locker creates locker of the given key.
func (c *LockerManager) Locker(key Key) (Locker, error) {
	return &internalLocker{
		key.String(),
		c.locks,
	}, nil
}

// A Locker represents an object that can be locked and unlocked.
type Locker interface {
	// Lock locks the mutex with a cancelable context
	Lock() error

	// TryLock locks the mutex if not already locked by another session.
	TryLock() error

	// Unlock unlocks the mutex.
	Unlock() error

	// RLock acquires a read lock with a cancelable context.
	RLock() error

	// RUnlock releases a read lock previously acquired by RLock.
	RUnlock() error
}

type internalLocker struct {
	key   string
	locks *locker.Locker
}

// Lock locks the mutex.
func (il *internalLocker) Lock() error {
	il.locks.Lock(il.key)

	return nil
}

// TryLock locks the mutex if not already locked by another session.
func (il *internalLocker) TryLock() error {
	if !il.locks.TryLock(il.key) {
		return ErrAlreadyLocked
	}

	return nil
}

// Unlock unlocks the mutex.
func (il *internalLocker) Unlock() error {
	if err := il.locks.Unlock(il.key); err != nil {
		return err
	}

	return nil
}

// RLock locks the mutex for reading..
func (il *internalLocker) RLock() error {
	il.locks.RLock(il.key)

	return nil
}

// RUnlock unlocks the read lock.
func (il *internalLocker) RUnlock() error {
	if err := il.locks.RUnlock(il.key); err != nil {
		return err
	}

	return nil
}
