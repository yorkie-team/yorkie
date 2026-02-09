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
	"strconv"
	gosync "sync"
	"sync/atomic"
	gotime "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/logging"
)

var publisherID loggerID

type loggerID int32

func (c *loggerID) next() string {
	next := atomic.AddInt32((*int32)(c), 1)
	return "p" + strconv.Itoa(int(next))
}

// EventFilter decides whether to deliver an event to a particular subscriber.
// Return true to skip (filter out) the event for this subscriber.
type EventFilter[E any] func(subscriber time.ActorID, event E) bool

// EnqueueFunc is called when a new event is enqueued. It can modify the event
// list (e.g. for deduplication). It returns the updated events slice and whether
// the new event was enqueued.
type EnqueueFunc[E any] func(events []E, newEvent E) ([]E, bool)

// BatchPublisher batches events and publishes them to subscribers at a
// configurable time interval.
type BatchPublisher[E any] struct {
	logger    *zap.SugaredLogger
	mutex     gosync.Mutex
	events    []E
	window    gotime.Duration
	closeChan chan struct{}
	subs      *Subscriptions[E]
	filter    EventFilter[E] // nil = no filtering
	onEnqueue EnqueueFunc[E] // nil = always enqueue
	onPublish func()         // nil = no-op; called after each publish batch
}

// BatchPublisherConfig holds optional configuration for BatchPublisher.
type BatchPublisherConfig[E any] struct {
	Filter    EventFilter[E]
	OnEnqueue EnqueueFunc[E]
	OnPublish func()
}

// NewBatchPublisher creates a new instance of BatchPublisher.
func NewBatchPublisher[E any](
	subs *Subscriptions[E],
	window gotime.Duration,
	config BatchPublisherConfig[E],
) *BatchPublisher[E] {
	bp := &BatchPublisher[E]{
		logger:    logging.New(publisherID.next()),
		window:    window,
		closeChan: make(chan struct{}),
		subs:      subs,
		filter:    config.Filter,
		onEnqueue: config.OnEnqueue,
		onPublish: config.OnPublish,
	}

	go bp.processLoop()
	return bp
}

// Publish adds the given event to the batch.
func (bp *BatchPublisher[E]) Publish(event E) {
	bp.mutex.Lock()
	defer bp.mutex.Unlock()

	if bp.onEnqueue != nil {
		newEvents, enqueued := bp.onEnqueue(bp.events, event)
		bp.events = newEvents
		if !enqueued {
			return
		}
	} else {
		bp.events = append(bp.events, event)
	}
}

func (bp *BatchPublisher[E]) processLoop() {
	ticker := gotime.NewTicker(bp.window)
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

func (bp *BatchPublisher[E]) publish() {
	bp.mutex.Lock()

	if len(bp.events) == 0 {
		bp.mutex.Unlock()
		return
	}

	events := bp.events
	bp.events = nil

	if bp.onPublish != nil {
		bp.onPublish()
	}

	bp.mutex.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		bp.logger.Infof(
			"Publishing batch of %d events for %s",
			len(events),
			bp.subs.Name(),
		)
	}

	for _, sub := range bp.subs.Values() {
		for _, event := range events {
			if bp.filter != nil && bp.filter(sub.Subscriber(), event) {
				continue
			}

			if ok := sub.Publish(event); !ok {
				bp.logger.Infof(
					"Publish to %s timeout or closed",
					sub.Subscriber(),
				)
			}
		}
	}
}

// Close stops the batch publisher.
func (bp *BatchPublisher[E]) Close() {
	close(bp.closeChan)
}
