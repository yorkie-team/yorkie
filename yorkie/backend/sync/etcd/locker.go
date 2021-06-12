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

	"github.com/pkg/errors"
	"go.etcd.io/etcd/clientv3/concurrency"

	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

// NewLocker creates locker of the given key.
func (c *Client) NewLocker(
	ctx context.Context,
	key sync.Key,
) (sync.Locker, error) {
	session, err := concurrency.NewSession(
		c.client,
		concurrency.WithContext(ctx),
		concurrency.WithTTL(c.config.LockLeaseTimeSec),
	)
	if err != nil {
		return nil, errors.WithStack(err)
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

// Lock locks a mutex.
func (il *internalLocker) Lock(ctx context.Context) error {
	if err := il.mu.Lock(ctx); err != nil {
		return errors.WithStack(err)
	}

	return nil
}

// Unlock unlocks the mutex.
func (il *internalLocker) Unlock(ctx context.Context) error {
	if err := il.mu.Unlock(ctx); err != nil {
		return errors.WithStack(err)
	}
	if err := il.session.Close(); err != nil {
		return errors.WithStack(err)
	}

	return nil
}
