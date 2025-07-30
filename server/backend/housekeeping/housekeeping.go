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

package housekeeping

import (
	"context"
	"fmt"
	"time"

	"github.com/go-co-op/gocron/v2"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Housekeeping is the housekeeping service. It periodically runs housekeeping
// tasks.
type Housekeeping struct {
	Config *Config

	scheduler  gocron.Scheduler
	leadership *LeadershipManager
}

// New creates a new housekeeping instance.
func New(conf *Config, db database.Database, nodeID string) (*Housekeeping, error) {
	scheduler, err := gocron.NewScheduler()
	if err != nil {
		return nil, fmt.Errorf("new scheduler: %w", err)
	}

	// Create leadership config from housekeeping config
	leaseDuration, err := conf.ParseLeadershipLeaseDuration()
	if err != nil {
		return nil, fmt.Errorf("parse leadership lease duration: %w", err)
	}

	renewalInterval, err := conf.ParseLeadershipRenewalInterval()
	if err != nil {
		return nil, fmt.Errorf("parse leadership renewal interval: %w", err)
	}

	leadershipConf := &LeadershipConfig{
		LeaseDuration:   leaseDuration,
		RenewalInterval: renewalInterval,
	}

	leadershipManager := NewLeadershipManager(db, nodeID, leadershipConf)

	return &Housekeeping{
		Config:     conf,
		scheduler:  scheduler,
		leadership: leadershipManager,
	}, nil
}

// RegisterTask registers task the housekeeping service.
func (h *Housekeeping) RegisterTask(
	interval time.Duration,
	task func(ctx context.Context) error,
) error {
	if _, err := h.scheduler.NewJob(
		gocron.DurationJob(interval),
		gocron.NewTask(func() {
			ctx := context.Background()

			if !h.leadership.IsLeader() {
				logging.From(ctx).Debug("skipping task - not leader")
				return
			}

			logging.From(ctx).Debugf("running housekeeping task with interval %s", interval)
			if err := task(ctx); err != nil {
				logging.From(ctx).Error(err)
			}
		}),
	); err != nil {
		return fmt.Errorf("scheduler new job: %w", err)
	}

	return nil
}

// Start starts the housekeeping service.
func (h *Housekeeping) Start(ctx context.Context) error {
	// Start the leadership manager
	if h.leadership != nil {
		if err := h.leadership.Start(ctx); err != nil {
			return fmt.Errorf("start leadership manager: %w", err)
		}
	}

	h.scheduler.Start()
	return nil
}

// Stop stops the housekeeping service.
func (h *Housekeeping) Stop() error {
	// Stop the leadership manager first
	if h.leadership != nil {
		h.leadership.Stop()
	}

	if err := h.scheduler.StopJobs(); err != nil {
		return fmt.Errorf("scheduler stop jobs: %w", err)
	}

	if err := h.scheduler.Shutdown(); err != nil {
		return fmt.Errorf("scheduler shutdown: %w", err)
	}

	return nil
}
