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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// Config is the configuration for StarRocks.
type Config struct {
	DSN string
}

// Warehouse represents the warehouse interface.
type Warehouse interface {
	// Close closes the warehouse.
	Close() error

	// GetActiveUsers returns the active users of the given project.
	GetActiveUsers(id types.ID, from, to time.Time) ([]types.MetricPoint, error)

	// GetActiveUsersCount returns the active users count of the given project.
	GetActiveUsersCount(id types.ID, from, to time.Time) (int, error)
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
