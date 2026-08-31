/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package warehouse implements the warehouse interface.
package warehouse

import (
	"context"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// Config is the configuration for StarRocks.
type Config struct {
	DSN string `yaml:"DSN"`

	// SummaryEnabled turns on the dual-read path that serves history from the
	// decoupled daily HLL summary tables and today from the base rollups. When
	// false, reads use the base rollups over the whole window, byte-identical to
	// the pre-summary behavior. It is flipped on only after the summary tables
	// are backfilled and validated. See docs/design/project-stats-long-retention.md.
	SummaryEnabled bool `yaml:"SummaryEnabled"`
}

// Warehouse represents the warehouse interface.
type Warehouse interface {
	// Close closes the warehouse.
	Close() error

	// GetActiveUsers returns the active users of the given project.
	GetActiveUsers(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetActiveUsersCount returns the active users count of the given project.
	GetActiveUsersCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)

	// GetActiveDocuments returns the active documents of the given project.
	GetActiveDocuments(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetActiveDocumentsCount returns the active documents count of the given project.
	GetActiveDocumentsCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)

	// GetActiveClients returns the active clients of the given project.
	GetActiveClients(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetActiveClientsCount returns the active clients count of the given project.
	GetActiveClientsCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)

	// GetActiveChannels returns the active channels of the given project.
	GetActiveChannels(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetActiveChannelsCount returns the active channels count of the given project.
	GetActiveChannelsCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)

	// GetSessions returns the sessions of the given project.
	GetSessions(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetSessionsCount returns the sessions count of the given project.
	GetSessionsCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)

	// GetPeakSessionsPerChannel returns the peak sessions per channel of the given project.
	GetPeakSessionsPerChannel(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) ([]types.MetricPoint, error)

	// GetPeakSessionsPerChannelCount returns the peak sessions per channel count of the given project.
	GetPeakSessionsPerChannelCount(
		ctx context.Context,
		id types.ID,
		from, to time.Time,
	) (int, error)
}

// Ensure creates a warehouse instance.
func Ensure(conf *Config) (Warehouse, error) {
	if conf == nil {
		return &DummyWarehouse{}, nil
	}

	rocks := &StarRocks{
		conf: conf,
	}

	if err := rocks.dial(conf.DSN); err != nil {
		return nil, err
	}

	return rocks, nil
}
