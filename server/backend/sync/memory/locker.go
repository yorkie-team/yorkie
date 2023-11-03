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

package memory

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/locker"
	"github.com/yorkie-team/yorkie/server/backend/sync"
)

type internalLocker struct {
	key   string
	locks *locker.Locker
}

// Lock locks the mutex.
func (il *internalLocker) Lock(_ context.Context) error {
	il.locks.Lock(il.key)

	return nil
}

// TryLock locks the mutex if not already locked by another session.
func (il *internalLocker) TryLock(_ context.Context) error {
	if !il.locks.TryLock(il.key) {
		return sync.ErrAlreadyLocked
	}

	return nil
}

// Unlock unlocks the mutex.
func (il *internalLocker) Unlock(_ context.Context) error {
	if err := il.locks.Unlock(il.key); err != nil {
		return err
	}

	return nil
}
