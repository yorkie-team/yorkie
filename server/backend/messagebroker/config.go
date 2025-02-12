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
	"errors"
	"fmt"
	"net/url"
	"strings"
)

var (
	// ErrEmptyAddress is returned when the address is empty.
	ErrEmptyAddress = errors.New("address cannot be empty")

	// ErrEmptyTopic is returned when the topic is empty.
	ErrEmptyTopic = errors.New("topic cannot be empty")
)

// Config is the configuration for creating a message broker instance.
type Config struct {
	Addresses string `yaml:"Addresses"`
	Topic     string `yaml:"Topic"`
}

// Validate validates this config.
func (c *Config) Validate() error {
	if c.Addresses == "" {
		return ErrEmptyAddress
	}

	kafkaAddresses := strings.Split(c.Addresses, ",")
	for _, addr := range kafkaAddresses {
		if addr == "" {
			return fmt.Errorf(`%s: %w`, c.Addresses, ErrEmptyAddress)
		}

		if _, err := url.Parse(addr); err != nil {
			return fmt.Errorf(`parse address "%s": %w`, c.Addresses, err)
		}
	}

	if c.Topic == "" {
		return ErrEmptyTopic
	}

	return nil
}
