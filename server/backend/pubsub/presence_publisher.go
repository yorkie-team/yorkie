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

package pubsub

import (
	gosync "sync"
	time "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/logging"
)

// PresencePublisher is a publisher that publishes presence events in batch.
type PresencePublisher struct {
	logger *zap.SugaredLogger
	mutex  gosync.Mutex
	events []events.PresenceEvent

	window    time.Duration
	closeChan chan struct{}
	subs      *PresenceSubscriptions
}

// NewPresenceBatchPublisher creates a new PresenceBatchPublisher instance.
func NewPresenceBatchPublisher(subs *PresenceSubscriptions, window time.Duration) *PresencePublisher {
	bp := &PresencePublisher{
		logger:    logging.New(id.next()),
		events:    nil,
		window:    window,
		closeChan: make(chan struct{}),
		subs:      subs,
	}

	go bp.processLoop()
	return bp
}

// Publish adds the given event to the batch.
func (bp *PresencePublisher) Publish(event events.PresenceEvent) {
	bp.mutex.Lock()
	defer bp.mutex.Unlock()

	bp.events = append(bp.events, event)
}

func (bp *PresencePublisher) processLoop() {
	ticker := time.NewTicker(bp.window)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			bp.publish()
		case <-bp.closeChan:
			bp.publish()
			return
		}
	}
}

func (bp *PresencePublisher) publish() {
	bp.mutex.Lock()

	if len(bp.events) == 0 {
		bp.mutex.Unlock()
		return
	}

	presenceEvents := bp.events
	bp.events = nil

	bp.mutex.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		bp.logger.Infof(
			"Publishing batch of %d presence events for key %s",
			len(presenceEvents),
			bp.subs.refKey,
		)
	}

	// Send all events to all subscribers
	for _, sub := range bp.subs.Values() {
		for _, event := range presenceEvents {
			// Skip sending broadcast events to the publisher themselves
			if event.Type == events.PresenceBroadcast && event.Publisher == sub.Subscriber() {
				continue
			}

			if ok := sub.Publish(event); !ok {
				bp.logger.Infof(
					"Publish presence event to %s timeout or closed",
					sub.Subscriber(),
				)
			}
		}
	}
}

// Close stops the batch publisher.
func (bp *PresencePublisher) Close() {
	close(bp.closeChan)
}
