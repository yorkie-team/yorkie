/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"encoding/json"
	"fmt"
	"log"
	gotime "time"

	"github.com/segmentio/kafka-go"

	"github.com/yorkie-team/yorkie/api/types/events"
)

// KafkaProducer is a producer for Kafka.
type KafkaProducer struct {
	writer *kafka.Writer
}

// NewKafkaProducer creates a new instance of KafkaProducer.
func NewKafkaProducer(kafkaURL string, topic string) *KafkaProducer {
	if kafkaURL == "" || topic == "" {
		return nil
	}

	return &KafkaProducer{
		writer: &kafka.Writer{
			Addr:     kafka.TCP(kafkaURL),
			Topic:    topic,
			Balancer: &kafka.LeastBytes{},
		},
	}
}

// ProduceMessage produces a message to Kafka.
func (kafkaWriter *KafkaProducer) ProduceMessage(ctx context.Context, message kafka.Message) error {
	if kafkaWriter == nil || kafkaWriter.writer == nil {
		return nil
	}
	err := kafkaWriter.writer.WriteMessages(ctx, message)
	if err != nil {
		return fmt.Errorf("write message to kafka: %v", err)
	}
	return nil
}

// ProduceUserEvent produces a user event to Kafka.
func (kafkaWriter *KafkaProducer) ProduceUserEvent(ctx context.Context, userID string,
	eventType events.Event, projectID string, userAgent string, metadata map[string]string) error {
	jsonMetadata, err := json.Marshal(metadata)
	if err != nil {
		log.Printf("could not marshal metadata  %v", err)
	}
	return kafkaWriter.ProduceMessage(ctx,
		kafka.Message{
			Value: []byte(fmt.Sprintf(`{
					"user_id": "%s",
					"timestamp": "%s",
					"event_type": "%s",
					"project_id": "%s",
					"user_agent": "%s",
					"metadata": %s
				}`, userID, gotime.Now(), eventType.GetType(), projectID, userAgent, jsonMetadata)),
		},
	)
}

// Close closes the KafkaProducer.
func (kafkaWriter *KafkaProducer) Close() error {
	err := kafkaWriter.writer.Close()
	if err != nil {
		return fmt.Errorf("close kafka writer: %v", err)
	}
	return nil
}
