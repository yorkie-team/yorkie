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

package etcd

import (
	"context"
	"fmt"

	"go.etcd.io/etcd/client/v3/concurrency"

	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// NewLocker creates locker of the given key.
func (c *Client) NewLocker(
	ctx context.Context,
	key sync.Key,
) (sync.Locker, error) {
	ttl := int(c.config.ParseLockLeaseTime().Seconds())
	session, err := concurrency.NewSession(
		c.client,
		concurrency.WithContext(ctx),
		concurrency.WithTTL(ttl),
	)
	if err != nil {
		logging.DefaultLogger().Error(err)
		return nil, fmt.Errorf("new session: %w", err)
	}

	return &internalLocker{
		session,
		concurrency.NewMutex(session, key.String()),
	}, nil
}

type internalLocker struct {
	session *concurrency.Session
	mu      *concurrency.Mutex
}

// Lock locks the mutex with a cancelable context
func (il *internalLocker) Lock(ctx context.Context) error {
	if err := il.mu.Lock(ctx); err != nil {
		logging.DefaultLogger().Error(err)
		return fmt.Errorf("lock: %w", err)
	}

	return nil
}

// TryLock locks the mutex if not already locked by another session.
func (il *internalLocker) TryLock(ctx context.Context) error {
	if err := il.mu.TryLock(ctx); err != nil {
		if err == concurrency.ErrLocked {
			return sync.ErrAlreadyLocked
		}

		logging.DefaultLogger().Error(err)
		return fmt.Errorf("try lock: %w", err)
	}

	return nil
}

// Unlock unlocks the mutex.
func (il *internalLocker) Unlock(ctx context.Context) error {
	if err := il.mu.Unlock(ctx); err != nil {
		logging.DefaultLogger().Error(err)
		return fmt.Errorf("unlock: %w", err)
	}

	if err := il.session.Close(); err != nil {
		return fmt.Errorf("close session: %w", err)
	}

	return nil
}
