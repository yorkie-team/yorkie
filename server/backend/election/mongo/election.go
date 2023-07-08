/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

// Package mongo is a mongo based implementation of election package.
package mongo

import (
	"context"
	"sync"
	"time"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Elector is a database-based implementation of election.Elector.
type Elector struct {
	database database.Database

	hostname string

	ctx        context.Context
	cancelFunc context.CancelFunc

	wg sync.WaitGroup
}

// NewElector creates a new elector instance.
func NewElector(
	hostname string,
	database database.Database,
) *Elector {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Elector{
		database: database,

		hostname: hostname,

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}
}

// StartElection starts leader election.
func (e *Elector) StartElection(
	leaseLockName string,
	leaseDuration time.Duration,
	onStartLeading func(ctx context.Context),
	onStoppedLeading func(),
) error {
	if err := e.database.CreateTTLIndex(context.Background(), leaseDuration); err != nil {
		return err
	}

	go e.run(leaseLockName, leaseDuration, onStartLeading, onStoppedLeading)
	return nil
}

// Stop stops all leader elections.
func (e *Elector) Stop() error {
	e.cancelFunc()
	e.wg.Wait()

	return nil
}

// run starts leader election loop.
// run will not return before leader election loop is stopped by ctx,
// or it has stopped holding the leader lease
func (e *Elector) run(
	leaseLockName string,
	leaseDuration time.Duration,
	onStartLeading func(ctx context.Context),
	onStoppedLeading func(),
) {
	for {
		ctx, cancelFunc := context.WithCancel(e.ctx)
		acquired, err := e.database.TryToAcquireLeaderLease(ctx, e.hostname, leaseLockName, leaseDuration)
		if err != nil {
			continue
		}

		if acquired {
			go func() {
				e.wg.Add(1)
				onStartLeading(ctx)
				e.wg.Done()
			}()
			logging.From(ctx).Infof(
				"leader elected: %s", e.hostname,
			)

			for {
				err = e.database.RenewLeaderLease(ctx, e.hostname, leaseLockName, leaseDuration)
				if err != nil {
					break
				}

				select {
				case <-time.After(leaseDuration / 2):
				case <-e.ctx.Done():
					cancelFunc()
					return
				}
			}
		} else {
			onStoppedLeading()
			logging.From(ctx).Infof(
				"leader lost: %s", e.hostname,
			)
		}

		select {
		case <-time.After(leaseDuration):
		case <-e.ctx.Done():
			cancelFunc()
			return
		}
	}
}
