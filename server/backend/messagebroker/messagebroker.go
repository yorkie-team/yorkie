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
	ProjectID string                 `json:"project_id"`
	EventType events.ClientEventType `json:"event_type"`
	UserID    string                 `json:"user_id"`
	Timestamp time.Time              `json:"timestamp"`
	UserAgent string                 `json:"user_agent"`
}

// ChannelEventsMessage represents a message for channel events
type ChannelEventsMessage struct {
	ProjectID  string                  `json:"project_id"`
	EventType  events.ChannelEventType `json:"event_type"`
	Timestamp  time.Time               `json:"timestamp"`
	ChannelKey string                  `json:"channel_key"`
}

// SessionEventsMessage represents a message for session events
type SessionEventsMessage struct {
	ProjectID  string                  `json:"project_id"`
	SessionID  string                  `json:"session_id"`
	Timestamp  time.Time               `json:"timestamp"`
	UserID     string                  `json:"user_id"`
	ChannelKey string                  `json:"channel_key"`
	EventType  events.ChannelEventType `json:"event_type"`
}

// Marshal marshals the user event message to JSON.
func (m UserEventMessage) Marshal() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	return encoded, nil
}

// Marshal marshals the channel events message to JSON.
func (m ChannelEventsMessage) Marshal() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	return encoded, nil
}

// Marshal marshals the session events message to JSON.
func (m SessionEventsMessage) Marshal() ([]byte, error) {
	encoded, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}

	return encoded, nil
}

// Brokers manages message brokers for different event types.
type Brokers struct {
	userEvents    Broker
	channelEvents Broker
	sessionEvents Broker
}

// UserEvents returns the broker for user events.
func (b *Brokers) UserEvents() Broker {
	return b.userEvents
}

// ChannelEvents returns the broker for channel events.
func (b *Brokers) ChannelEvents() Broker {
	return b.channelEvents
}

// SessionEvents returns the broker for session events.
func (b *Brokers) SessionEvents() Broker {
	return b.sessionEvents
}

// NewBrokers creates a new Brokers instance with the given brokers.
func NewBrokers(user, channel, session Broker) *Brokers {
	return &Brokers{
		userEvents:    user,
		channelEvents: channel,
		sessionEvents: session,
	}
}

// Broker is an interface for the message broker.
type Broker interface {
	Produce(ctx context.Context, msg Message) error
	Close() error
}

// Ensure creates a message broker based on the given configuration.
// If the configuration is nil or invalid, it returns a Brokers instance with
// DummyBroker for all fields, allowing callers to use the brokers without nil checks.
func Ensure(kafkaConf *Config) *Brokers {
	dummy := &DummyBroker{}

	if kafkaConf == nil {
		return &Brokers{
			userEvents:    dummy,
			channelEvents: dummy,
			sessionEvents: dummy,
		}
	}

	if err := kafkaConf.Validate(); err != nil {
		logging.DefaultLogger().Warnf("invalid kafka configuration: %v", err)
		return &Brokers{
			userEvents:    dummy,
			channelEvents: dummy,
			sessionEvents: dummy,
		}
	}

	topics := []string{
		kafkaConf.UserEventsTopic,
		kafkaConf.ChannelEventsTopic,
		kafkaConf.SessionEventsTopic,
	}

	logging.DefaultLogger().Infof(
		"connecting to kafka: %s, topics: %s",
		kafkaConf.Addresses,
		strings.Join(topics, ","),
	)

	brokers := &Brokers{
		userEvents:    dummy,
		channelEvents: dummy,
		sessionEvents: dummy,
	}

	if kafkaConf.UserEventsTopic != "" {
		brokers.userEvents = newKafkaBroker(kafkaConf, kafkaConf.UserEventsTopic)
	}
	if kafkaConf.ChannelEventsTopic != "" {
		brokers.channelEvents = newKafkaBroker(kafkaConf, kafkaConf.ChannelEventsTopic)
	}
	if kafkaConf.SessionEventsTopic != "" {
		brokers.sessionEvents = newKafkaBroker(kafkaConf, kafkaConf.SessionEventsTopic)
	}

	return brokers
}
