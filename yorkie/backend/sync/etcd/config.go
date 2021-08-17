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

package etcd

import (
	"errors"
	"time"
)

var (
	//ErrEmptyEndpoints occurs when the endpoints in the config is empty.
	ErrEmptyEndpoints = errors.New("length of etcd endpoints must be greater than 0")
)

// Config is the configuration for creating a Client instance.
type Config struct {
	Endpoints      []string      `json:"Endpoints"`
	DialTimeoutSec time.Duration `json:"DialTimeoutSec"`
	Username       string        `json:"Username"`
	Password       string        `json:"Password"`

	LockLeaseTimeSec int `json:"LockLeaseTimeSec"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	if len(c.Endpoints) == 0 {
		return ErrEmptyEndpoints
	}

	return nil
}
