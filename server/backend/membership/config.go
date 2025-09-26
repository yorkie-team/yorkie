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
package membership

import (
	"fmt"
	"time"
)

// Config contains configuration for membership (formerly leadership) management.
type Config struct {
	LeaseDuration   string `yaml:"LeaseDuration"`
	RenewalInterval string `yaml:"RenewalInterval"`
}

// ParseLeaseDuration parses the lease duration.
func (c *Config) ParseLeaseDuration() (time.Duration, error) {
	duration, err := time.ParseDuration(c.LeaseDuration)
	if err != nil {
		return 0, fmt.Errorf("parse lease duration %s: %w", c.LeaseDuration, err)
	}

	return duration, nil
}

// ParseRenewalInterval parses the renewal interval.
func (c *Config) ParseRenewalInterval() (time.Duration, error) {
	interval, err := time.ParseDuration(c.RenewalInterval)
	if err != nil {
		return 0, fmt.Errorf("parse renewal interval %s: %w", c.RenewalInterval, err)
	}

	return interval, nil
}
