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

	"github.com/yorkie-team/yorkie/server/logging"
)

/* existing commit

database    database.Database
coordinator sync.Coordinator

intervalDeactivateCandidates                 time.Duration
intervalDeleteDocuments                      time.Duration
documentHardDeletionGracefulPeriod           time.Duration
clientDeactivationCandidateLimitPerProject   int
DocumentHardDeletionCandidateLimitPerProject int
projectFetchSize                             int
*/

// Housekeeping is the housekeeping service. It periodically runs housekeeping tasks.
type Housekeeping struct {
	Config    *Config
	scheduler gocron.Scheduler
}

// New creates a new housekeeping instance.
func New(conf *Config) (*Housekeeping, error) {
	scheduler, err := gocron.NewScheduler()
	/* existing commit
	func New(
		conf *Config,
		database database.Database,
		coordinator sync.Coordinator,
	) (*Housekeeping, error) {
		intervalDeactivateCandidates, err := time.ParseDuration(conf.IntervalDeactivateCandidates)
		if err != nil {
			return nil, fmt.Errorf("parse intervalDeactivateCandidates %s: %w",
				conf.IntervalDeactivateCandidates, err)
		}

		intervalDeleteDocuments, err := time.ParseDuration(conf.IntervalDeleteDocuments)
		if err != nil {
	*/
	if err != nil {
		return nil, fmt.Errorf("new scheduler: %w", err)
	}
	/* existing commit
		return nil, fmt.Errorf("parse intervalDeleteDocuments %s: %w", conf.IntervalDeleteDocuments, err)
	}

	documentHardDeletionGracefulPeriod, err := time.ParseDuration(conf.DocumentHardDeletionGracefulPeriod)
	if err != nil {
		return nil, fmt.Errorf("parse documentHardDeletionGracefulPeriod %s: %w",
			conf.DocumentHardDeletionGracefulPeriod, err)
	}
	*/
	return &Housekeeping{
		Config:    conf,
		scheduler: scheduler,
		/* existing commit
		database:    database,
		coordinator: coordinator,

		intervalDeactivateCandidates:                 intervalDeactivateCandidates,
		intervalDeleteDocuments:                      intervalDeleteDocuments,
		documentHardDeletionGracefulPeriod:           documentHardDeletionGracefulPeriod,
		clientDeactivationCandidateLimitPerProject:   conf.ClientDeactivationCandidateLimitPerProject,
		DocumentHardDeletionCandidateLimitPerProject: conf.DocumentHardDeletionCandidateLimitPerProject,
		projectFetchSize:                             conf.ProjectFetchSize,

		ctx:        ctx,
		cancelFunc: cancelFunc,
		*/
	}, nil
}

// RegisterTask registers task the housekeeping service.
func (h *Housekeeping) RegisterTask(
	intervalDeactivateCandidates time.Duration,
	intervalDeleteDocuments time.Duration,
	task func(ctx context.Context) error,
) error {
	if _, err := h.scheduler.NewJob(
		gocron.DurationJob(intervalDeactivateCandidates),
		gocron.NewTask(func() {
			ctx := context.Background()
			if err := task(ctx); err != nil {
				logging.From(ctx).Error(err)
			}
		}),
	); err != nil {
		return fmt.Errorf("scheduler new job: %w", err)
	}

	if _, err := h.scheduler.NewJob(
		gocron.DurationJob(intervalDeleteDocuments),
		gocron.NewTask(func() {
			ctx := context.Background()
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
func (h *Housekeeping) Start() error {
	h.scheduler.Start()
	return nil
}

// Stop stops the housekeeping service.
func (h *Housekeeping) Stop() error {
	if err := h.scheduler.StopJobs(); err != nil {
		return fmt.Errorf("scheduler stop jobs: %w", err)
	}

	if err := h.scheduler.Shutdown(); err != nil {
		return fmt.Errorf("scheduler shutdown: %w", err)
	}
	return nil
}
