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

package messagebroker

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/yorkie-team/yorkie/server/logging"
)

// Config is the configuration for creating a message broker instance.
type Config struct {
	Addresses string `yaml:"Addresses"`
	Topic     string `yaml:"Topic"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	// TODO(hackerwins): Implement this.

	logging.DefaultLogger().Infof("Config: %+v", c)

	if c.Addresses == "" {
		return fmt.Errorf("address cannot be empty")
	}

	kafkaAddresses := strings.Split(c.Addresses, ",")
	for _, addr := range kafkaAddresses {
		if addr == "" {
			return fmt.Errorf(`address cannot be empty: "%s" in "%s"`, addr, c.Addresses)
		}

		if _, err := url.Parse(addr); err != nil {
			return fmt.Errorf(`invalid argument "%s" for "--kafka-addresses" flag`, c.Addresses)
		}
	}

	if c.Topic == "" {
		return fmt.Errorf("topic cannot not be empty")
	}

	return nil
}
