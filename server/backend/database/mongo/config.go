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

package mongo

import (
	"fmt"
	"os"
	"time"
)

// Config is the configuration for creating a Client instance.
type Config struct {
	ConnectionTimeout string `yaml:"ConnectionTimeout"`
	ConnectionURI     string `yaml:"ConnectionURI"`
	YorkieDatabase    string `yaml:"YorkieDatabase"`
	PingTimeout       string `yaml:"PingTimeout"`
}

// Validate returns an error if the provided Config is invalidated.
func (c *Config) Validate() error {
	if _, err := time.ParseDuration(c.ConnectionTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-connection-timeout" flag: %w`,
			c.ConnectionTimeout,
			err,
		)
	}

	if _, err := time.ParseDuration(c.PingTimeout); err != nil {
		return fmt.Errorf(
			`invalid argument "%s" for "--mongo-ping-timeout" flag: %w`,
			c.PingTimeout,
			err,
		)
	}

	return nil
}

// ParseConnectionTimeout returns connection timeout duration.
func (c *Config) ParseConnectionTimeout() time.Duration {
	result, err := time.ParseDuration(c.ConnectionTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse connection timeout: %w", err)
		os.Exit(1)
	}

	return result
}

// ParsePingTimeout returns ping timeout duration.
func (c *Config) ParsePingTimeout() time.Duration {
	result, err := time.ParseDuration(c.PingTimeout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse ping timeout: %w", err)
		os.Exit(1)
	}

	return result
}
