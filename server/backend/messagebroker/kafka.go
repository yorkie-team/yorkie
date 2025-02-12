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
	writer *kafka.Writer
}

// newKafkaBroker creates a new instance of KafkaProducer.
func newKafkaBroker(addresses []string, topic string) *KafkaBroker {
	return &KafkaBroker{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(addresses...),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
			Async:    true,
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
		return fmt.Errorf("marshal message: %v", err)
	}

	// TODO(hackerwins): Consider using message batching.
	if err := mb.writer.WriteMessages(ctx, kafka.Message{Value: value}); err != nil {
		return fmt.Errorf("write message to kafka: %v", err)
	}

	return nil
}

// Close closes the KafkaProducer.
func (mb *KafkaBroker) Close() error {
	if err := mb.writer.Close(); err != nil {
		return fmt.Errorf("close kafka writer: %v", err)
	}

	return nil
}
