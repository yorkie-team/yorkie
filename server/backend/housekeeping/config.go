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

// Package housekeeping is the package for housekeeping service. It cleans up
// the resources and data that is no longer needed.
package housekeeping

import (
	"fmt"
	"time"
)

// Config is the configuration for the housekeeping service.
type Config struct {
	// Interval is the time between housekeeping runs.
	Interval string `yaml:"Interval"`

	// CandidatesLimitPerProject is the maximum number of candidates to be returned per project.
	CandidatesLimitPerProject int `yaml:"CandidatesLimitPerProject"`

	// ProjectFetchSize is the maximum number of projects to be returned to deactivate candidates.
	ProjectFetchSize int `yaml:"HousekeepingProjectFetchSize"`
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

	if c.CandidatesLimitPerProject <= 0 {
		return fmt.Errorf(
			`invalid argument %d for "--housekeeping-candidates-limit-per-project" flag`,
			c.ProjectFetchSize,
		)
	}

	if c.ProjectFetchSize <= 0 {
		return fmt.Errorf(
			`invalid argument %d for "--housekeeping-project-fetc-size" flag`,
			c.ProjectFetchSize,
		)
	}

	return nil
}

// ParseInterval parses the interval.
func (c *Config) ParseInterval() (time.Duration, error) {
	interval, err := time.ParseDuration(c.Interval)
	if err != nil {
		return 0, fmt.Errorf("parse interval %s: %w", c.Interval, err)
	}

	return interval, nil
}
