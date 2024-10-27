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

package memory

import (
	"context"
	gosync "sync"
	time "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/server/logging"
)

// BatchPublisher is a publisher that publishes events in batch.
type BatchPublisher struct {
	events    []sync.DocEvent
	mutex     gosync.Mutex
	window    time.Duration
	maxBatch  int
	closeChan chan struct{}
	subs      *Subscriptions
}

// NewBatchPublisher creates a new BatchPublisher instance.
func NewBatchPublisher(window time.Duration, maxBatch int, subs *Subscriptions) *BatchPublisher {
	bp := &BatchPublisher{
		window:    window,
		maxBatch:  maxBatch,
		closeChan: make(chan struct{}),
		subs:      subs,
	}

	go bp.processLoop()
	return bp
}

// Publish adds the given event to the batch. If the batch is full, it publishes
// the batch.
func (bp *BatchPublisher) Publish(ctx context.Context, event sync.DocEvent) {
	bp.mutex.Lock()
	defer bp.mutex.Unlock()

	// TODO(hackerwins): If the event is DocumentChangedEvent, we should merge
	// the events to reduce the number of events to be published.
	bp.events = append(bp.events, event)

	// TODO(hackerwins): Consider to use processLoop to publish events.
	if len(bp.events) >= bp.maxBatch {
		bp.publish(ctx)
	}
}

func (bp *BatchPublisher) processLoop() {
	ticker := time.NewTicker(bp.window)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bp.mutex.Lock()
			bp.publish(context.Background())
			bp.mutex.Unlock()
		case <-bp.closeChan:
			return
		}
	}
}

func (bp *BatchPublisher) publish(ctx context.Context) {
	if len(bp.events) == 0 {
		return
	}

	if logging.Enabled(zap.DebugLevel) {
		logging.From(ctx).Infof(
			"Publishing batch of %d events for document %s",
			len(bp.events),
			bp.subs.docKey,
		)
	}

	for _, sub := range bp.subs.Values() {
		for _, event := range bp.events {
			if sub.Subscriber().Compare(event.Publisher) == 0 {
				continue
			}

			if ok := sub.Publish(event); !ok {
				logging.From(ctx).Infof(
					"Publish(%s,%s) to %s timeout or closed",
					event.Type,
					event.Publisher,
					sub.Subscriber(),
				)
			}
		}
	}

	bp.events = nil
}

// Close stops the batch publisher
func (bp *BatchPublisher) Close() {
	close(bp.closeChan)
}
