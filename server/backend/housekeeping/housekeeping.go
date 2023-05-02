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

// Package housekeeping provides the housekeeping service. The housekeeping
// service is responsible for deactivating clients that have not been used for
// a long time.
package housekeeping

import (
	"context"
	"fmt"
	"time"

	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/backend/election"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/clients"
	"github.com/yorkie-team/yorkie/server/logging"
)

const (
	deactivateCandidatesKey = "housekeeping/deactivateCandidates"
	lockLeaseName           = "housekeeping"
)

// Config is the configuration for the housekeeping service.
type Config struct {
	// Interval is the time between housekeeping runs.
	Interval string `yaml:"Interval"`

	// CandidatesLimitPerProject is the maximum number of candidates to be returned per project.
	CandidatesLimitPerProject int `yaml:"CandidatesLimitPerProject"`

	// LeaseDuration is the duration that non-leader candidates will wait to force acquire leadership.
	LeaseDuration string `yaml:"LeaseDuration"`
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.Interval); err != nil {
		return fmt.Errorf(
			`invalid argument %s for "--housekeeping-interval" flag: %w`,
			c.Interval,
			err,
		)
	}

	if _, err := time.ParseDuration(c.LeaseDuration); err != nil {
		return fmt.Errorf(
			`invalid argument %s for "--housekeeping-lease-duration" flag: %w`,
			c.LeaseDuration,
			err,
		)
	}

	return nil
}

// Housekeeping is the housekeeping service. It periodically runs housekeeping
// tasks. It is responsible for deactivating clients that have not been active
// for a long time.
type Housekeeping struct {
	database    database.Database
	coordinator sync.Coordinator
	elector     *election.Elector

	interval                  time.Duration
	candidatesLimitPerProject int
	leaseDuration             time.Duration

	ctx        context.Context
	cancelFunc context.CancelFunc
}

// Start starts the housekeeping service.
func Start(
	conf *Config,
	database database.Database,
	coordinator sync.Coordinator,
	elector *election.Elector,
) (*Housekeeping, error) {
	h, err := New(conf, database, coordinator, elector)
	if err != nil {
		return nil, err
	}
	if err := h.Start(); err != nil {
		return nil, err
	}

	return h, nil
}

// New creates a new housekeeping instance.
func New(
	conf *Config,
	database database.Database,
	coordinator sync.Coordinator,
	elector *election.Elector,
) (*Housekeeping, error) {
	interval, err := time.ParseDuration(conf.Interval)
	if err != nil {
		return nil, fmt.Errorf("parse interval %s: %w", conf.Interval, err)
	}
	leaseDuration, err := time.ParseDuration(conf.LeaseDuration)
	if err != nil {
		return nil, fmt.Errorf("parse lease duration %s: %w", conf.LeaseDuration, err)
	}

	ctx, cancelFunc := context.WithCancel(context.Background())

	return &Housekeeping{
		database:    database,
		coordinator: coordinator,
		elector:     elector,

		interval:                  interval,
		candidatesLimitPerProject: conf.CandidatesLimitPerProject,
		leaseDuration:             leaseDuration,

		ctx:        ctx,
		cancelFunc: cancelFunc,
	}, nil
}

// Start starts the housekeeping service.
func (h *Housekeeping) Start() error {
	if h.elector != nil {
		err := h.elector.Start(
			lockLeaseName,
			h.leaseDuration,
			func(ctx context.Context) { h.run() },
			func() { h.cancelFunc() },
		)
		if err != nil {
			return err
		}
	} else {
		go h.run()
	}

	return nil
}

// Stop stops the housekeeping service.
func (h *Housekeeping) Stop() error {
	h.cancelFunc()

	return nil
}

// run is the housekeeping loop.
func (h *Housekeeping) run() {
	for {
		ctx := context.Background()
		if err := h.deactivateCandidates(ctx); err != nil {
			continue
		}

		select {
		case <-time.After(h.interval):
		case <-h.ctx.Done():
			return
		}
	}
}

// deactivateCandidates deactivates candidates.
func (h *Housekeeping) deactivateCandidates(ctx context.Context) error {
	start := time.Now()
	locker, err := h.coordinator.NewLocker(ctx, deactivateCandidatesKey)
	if err != nil {
		return err
	}

	if err := locker.Lock(ctx); err != nil {
		return err
	}

	defer func() {
		if err := locker.Unlock(ctx); err != nil {
			logging.From(ctx).Error(err)
		}
	}()

	candidates, err := h.database.FindDeactivateCandidates(
		ctx,
		h.candidatesLimitPerProject,
	)
	if err != nil {
		return err
	}

	deactivatedCount := 0
	for _, clientInfo := range candidates {
		if _, err := clients.Deactivate(
			ctx,
			h.database,
			clientInfo.ProjectID,
			clientInfo.ID,
		); err != nil {
			return err
		}

		deactivatedCount++
	}

	if len(candidates) > 0 {
		logging.From(ctx).Infof(
			"HSKP: candidates %d, deactivated %d, %s",
			len(candidates),
			deactivatedCount,
			time.Since(start),
		)
	}

	return nil
}
