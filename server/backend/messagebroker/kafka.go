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
	"context"
	"fmt"

	"github.com/segmentio/kafka-go"
)

// KafkaBroker is a producer for Kafka.
type KafkaBroker struct {
	conf   *Config
	writer *kafka.Writer
}

// newKafkaBroker creates a new instance of KafkaProducer.
func newKafkaBroker(conf *Config) *KafkaBroker {
	return &KafkaBroker{
		conf: conf,
		writer: &kafka.Writer{
			Addr:         kafka.TCP(conf.SplitAddresses()...),
			Topic:        conf.Topic,
			WriteTimeout: conf.MustParseWriteTimeout(),
			Balancer:     &kafka.LeastBytes{},
			Async:        true,
		},
	}
}

// Produce produces a user event to Kafka.
func (mb *KafkaBroker) Produce(
	ctx context.Context,
	msg Message,
) error {
	value, err := msg.Marshal()
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}

	if err := mb.writer.WriteMessages(ctx, kafka.Message{Value: value}); err != nil {
		return fmt.Errorf("write message to kafka: %w", err)
	}

	return nil
}

// Close closes the KafkaProducer.
func (mb *KafkaBroker) Close() error {
	if err := mb.writer.Close(); err != nil {
		return fmt.Errorf("close kafka writer: %w", err)
	}

	return nil
}
