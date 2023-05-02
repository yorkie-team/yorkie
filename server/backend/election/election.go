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

// Package election provides leader election between server cluster. It is used to
// elect leader among server cluster and run tasks only on the leader.
package election

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Elector is responsible for leader election between server cluster.
type Elector struct {
	database database.Database
	hostname string

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// New creates a new elector instance.
func New(
	hostname string,
	database database.Database,
) (*Elector, error) {
	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Elector{
		database: database,
		hostname: hostname,

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}, nil
}

// Start starts leader election.
func (e *Elector) Start(
	leaseLockName string,
	leaseDuration time.Duration,
	onStartLeading func(ctx context.Context),
	onStoppedLeading func(),
) error {
	if err := e.database.CreateTTLIndex(context.Background(), leaseDuration); err != nil {
		return err
	}

	go e.Run(leaseLockName, leaseDuration, onStartLeading, onStoppedLeading)
	return nil
}

// Stop stops leader election.
func (e *Elector) Stop() error {
	e.cancelFunc()

	return nil
}

// Run starts leader election loop.
// Run will not return before leader election loop is stopped by ctx,
// or it has stopped holding the leader lease
func (e *Elector) Run(
	leaseLockName string,
	leaseDuration time.Duration,
	onStartLeading func(ctx context.Context),
	onStoppedLeading func(),
) {
	for {
		ctx := context.Background()
		acquired, err := e.database.TryToAcquireLeaderLease(ctx, e.hostname, leaseLockName, leaseDuration)
		if err != nil {
			continue
		}

		if acquired {
			go onStartLeading(ctx)
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
			return
		}
	}
}
