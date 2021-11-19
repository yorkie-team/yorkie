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

package sync

import (
	"context"
	"errors"
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

// A Locker represents an object that can be locked and unlocked.
type Locker interface {
	// Lock locks the mutex with a cancelable context
	Lock(ctx context.Context) error

	// TryLock locks the mutex if not already locked by another session.
	TryLock(ctx context.Context) error

	// Unlock unlocks the mutex.
	Unlock(ctx context.Context) error
}
