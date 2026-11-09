/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

type loggerID struct{ n atomic.Int32 }

func (c *loggerID) next() string {
	next := c.n.Add(1)
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
	window    atomic.Int64 // gotime.Duration
	closeChan chan struct{}
	subs      *Subscriptions[E]
	filter    EventFilter[E] // nil = no filtering
	onEnqueue EnqueueFunc[E] // nil = always enqueue
	onPublish func()         // nil = no-op; called after each publish batch

	// throttle selects throttleLoop over tickLoop; pending wakes it.
	throttle bool
	pending  chan struct{}
}

// BatchPublisherConfig holds optional configuration for BatchPublisher.
type BatchPublisherConfig[E any] struct {
	Filter    EventFilter[E]
	OnEnqueue EnqueueFunc[E]
	OnPublish func()

	// Throttle publishes an event that arrives on an idle publisher at once
	// and then at most one batch per window, instead of one batch every
	// window on a ticker. An idle throttled publisher does not wake up.
	Throttle bool
}

// NewBatchPublisher creates a new instance of BatchPublisher.
func NewBatchPublisher[E any](
	subs *Subscriptions[E],
	window gotime.Duration,
	config BatchPublisherConfig[E],
) *BatchPublisher[E] {
	bp := &BatchPublisher[E]{
		logger:    logging.New(publisherID.next()),
		closeChan: make(chan struct{}),
		subs:      subs,
		filter:    config.Filter,
		onEnqueue: config.OnEnqueue,
		onPublish: config.OnPublish,
		throttle:  config.Throttle,
	}
	bp.window.Store(int64(window))

	if bp.throttle {
		bp.pending = make(chan struct{}, 1)
		go bp.throttleLoop()
	} else {
		go bp.tickLoop()
	}
	return bp
}

// Window returns the current batch window.
func (bp *BatchPublisher[E]) Window() gotime.Duration {
	return gotime.Duration(bp.window.Load())
}

// SetWindow changes the batch window of a throttled publisher. It times the
// windows that start after the call; one already running ends as it was set.
// A ticking publisher keeps the period it started with.
func (bp *BatchPublisher[E]) SetWindow(window gotime.Duration) {
	bp.window.Store(int64(window))
}

// Publish adds the given event to the batch.
func (bp *BatchPublisher[E]) Publish(event E) {
	bp.mutex.Lock()
	if bp.onEnqueue != nil {
		// Whether the event was kept or folded into another, the returned
		// slice is the batch; nothing else here depends on which.
		bp.events, _ = bp.onEnqueue(bp.events, event)
	} else {
		bp.events = append(bp.events, event)
	}
	bp.mutex.Unlock()

	if bp.throttle {
		// A wake-up already pending covers this event too.
		select {
		case bp.pending <- struct{}{}:
		default:
		}
	}
}

// throttleLoop publishes when an event arrives, unless the last publish was
// less than a window ago; then a timer publishes everything that arrived in
// the meantime once the window has passed. With no events it only waits.
func (bp *BatchPublisher[E]) throttleLoop() {
	var timer *gotime.Timer
	var fire <-chan gotime.Time // non-nil while the timer is armed
	var last gotime.Time

	for {
		select {
		case <-bp.pending:
			if fire != nil {
				continue // the armed timer publishes this event with the rest
			}
			if wait := bp.Window() - gotime.Since(last); wait > 0 {
				timer = gotime.NewTimer(wait)
				fire = timer.C
				continue
			}
		case <-fire:
			fire = nil
		case <-bp.closeChan:
			if timer != nil {
				timer.Stop()
			}
			bp.publish()
			return
		}

		last = gotime.Now()
		bp.publish()
	}
}

// tickLoop publishes whatever arrived every window.
func (bp *BatchPublisher[E]) tickLoop() {
	ticker := gotime.NewTicker(bp.Window())
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

	events := bp.events
	bp.events = nil

	if len(events) > 0 && bp.onPublish != nil {
		bp.onPublish()
	}

	bp.mutex.Unlock()

	if len(events) > 0 && logging.Enabled(zap.DebugLevel) {
		bp.logger.Infof(
			"Publishing batch of %d events for %s",
			len(events),
			bp.subs.Name(),
		)
	}

	// Iterate even when the batch is empty so dead subscriptions
	// (self-pruned after consecutive Publish failures) are reaped.
	var deadIDs []string
	for _, sub := range bp.subs.Values() {
		if sub.IsDead() {
			deadIDs = append(deadIDs, sub.ID())
			continue
		}
		for _, event := range events {
			if bp.filter != nil && bp.filter(sub.Subscriber(), event) {
				continue
			}

			if ok := sub.Publish(event); !ok {
				bp.logger.Infof(
					"Publish to %s timeout or closed",
					sub.Subscriber(),
				)
				if sub.IsDead() {
					deadIDs = append(deadIDs, sub.ID())
					break
				}
			}
		}
	}
	for _, id := range deadIDs {
		bp.subs.Delete(id)
	}
}

// Close stops the batch publisher.
func (bp *BatchPublisher[E]) Close() {
	close(bp.closeChan)
}
