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

// Package profiling provides profiling server.
package profiling

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidProfilingPort occurs when the port in the config is invalid.
	ErrInvalidProfilingPort = errors.New("invalid port number for profiling server")
)

// Config is the configuration for creating a Server instance.
type Config struct {
	Port        int  `yaml:"Port"`
	EnablePprof bool `yaml:"EnablePprof"`
}

// Validate validates the port number.
func (c *Config) Validate() error {
	if c.Port < 1 || 65535 < c.Port {
		return fmt.Errorf("must be between 1 and 65535, given %d: %w", c.Port, ErrInvalidProfilingPort)
	}

	return nil
}
