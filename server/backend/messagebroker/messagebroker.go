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

// Package messagebroker provides the message broker implementation of the Yorkie.
package messagebroker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/logging"
)

// Message represents a message that can be sent to the message broker.
type Message interface {
	Marshal() ([]byte, error)
}

// UserEventMessage represents a message for user events
type UserEventMessage struct {
	UserID    string                 `json:"user_id"`
	Timestamp time.Time              `json:"timestamp"`
	EventType events.ClientEventType `json:"event_type"`
	ProjectID string                 `json:"project_id"`
	UserAgent string                 `json:"user_agent"`
}

// Marshal marshals the user event message to JSON.
func (m UserEventMessage) Marshal() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	return encoded, nil
}

// Broker is an interface for the message broker.
type Broker interface {
	Produce(ctx context.Context, msg Message) error
	Close() error
}

// Ensure creates a message broker based on the given configuration.
func Ensure(kafkaConf *Config) Broker {
	if kafkaConf == nil {
		return &DummyBroker{}
	}

	if err := kafkaConf.Validate(); err != nil {
		return &DummyBroker{}
	}

	logging.DefaultLogger().Infof(
		"connecting to kafka: %s, topic: %s",
		kafkaConf.Addresses,
		kafkaConf.Topic,
	)

	return newKafkaBroker(strings.Split(kafkaConf.Addresses, ","), kafkaConf.Topic)
}
