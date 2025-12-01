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

// Package messaging provides the message broker implementation of the Yorkie.
package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/logging"
)

// EventType represents the type of event to be published.
type EventType string

const (
	// UserEventsType represents user-related events.
	UserEventsType EventType = "user"

	// ChannelEventsType represents channel-related events.
	ChannelEventsType EventType = "channel"

	// SessionEventsType represents session-related events.
	SessionEventsType EventType = "session"
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
	return marshalMessage(m)
}

// Marshal marshals the channel events message to JSON.
func (m ChannelEventsMessage) Marshal() ([]byte, error) {
	return marshalMessage(m)
}

// Marshal marshals the session events message to JSON.
func (m SessionEventsMessage) Marshal() ([]byte, error) {
	return marshalMessage(m)
}

// marshalMessage is a helper function to marshal any message to JSON.
func marshalMessage(msg any) ([]byte, error) {
	encoded, err := json.Marshal(msg)
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

// Manager manages message brokers for different event types.
type Manager struct {
	brokers map[EventType]Broker
}

// Ensure creates a message broker based on the given configuration.
// If the configuration is nil or invalid, it returns a Manager instance with
// DummyBroker for all event types, allowing callers to use the brokers without nil checks.
func Ensure(kafkaConf *Config) Broker {
	dummy := &DummyBroker{}

	if kafkaConf == nil {
		return newManagerWithDummy(dummy)
	}

	if err := kafkaConf.Validate(); err != nil {
		logging.DefaultLogger().Warnf("invalid kafka configuration: %v", err)
		return newManagerWithDummy(dummy)
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

	brokers := make(map[EventType]Broker)

	if kafkaConf.UserEventsTopic != "" {
		brokers[UserEventsType] = newKafkaBroker(kafkaConf, kafkaConf.UserEventsTopic)
	} else {
		brokers[UserEventsType] = dummy
	}

	if kafkaConf.ChannelEventsTopic != "" {
		brokers[ChannelEventsType] = newKafkaBroker(kafkaConf, kafkaConf.ChannelEventsTopic)
	} else {
		brokers[ChannelEventsType] = dummy
	}

	if kafkaConf.SessionEventsTopic != "" {
		brokers[SessionEventsType] = newKafkaBroker(kafkaConf, kafkaConf.SessionEventsTopic)
	} else {
		brokers[SessionEventsType] = dummy
	}

	return &Manager{brokers: brokers}
}

// newManagerWithDummy creates a new Manager with dummy brokers for all event types.
func newManagerWithDummy(dummy *DummyBroker) *Manager {
	return &Manager{
		brokers: map[EventType]Broker{
			UserEventsType:    dummy,
			ChannelEventsType: dummy,
			SessionEventsType: dummy,
		},
	}
}

// NewBroker creates a new Manager with the specified brokers for each event type.
// This is primarily used for testing purposes.
func NewBroker(user, channel, session Broker) *Manager {
	return &Manager{
		brokers: map[EventType]Broker{
			UserEventsType:    user,
			ChannelEventsType: channel,
			SessionEventsType: session,
		},
	}
}

// Close closes all the brokers.
func (m *Manager) Close() error {
	var errs []error

	for _, broker := range m.brokers {
		if err := broker.Close(); err != nil {
			errs = append(errs, err)
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("closing brokers: %v", errs)
	}

	return nil
}

// Produce produces an event to the appropriate message broker based on message type.
func (m *Manager) Produce(ctx context.Context, msg Message) error {
	var eventType EventType

	switch msg.(type) {
	case UserEventMessage:
		eventType = UserEventsType
	case ChannelEventsMessage:
		eventType = ChannelEventsType
	case SessionEventsMessage:
		eventType = SessionEventsType
	default:
		return fmt.Errorf("unknown message type: %T", msg)
	}

	return m.brokers[eventType].Produce(ctx, msg)
}
