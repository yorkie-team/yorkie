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

// Package pubsub provides a pub-sub implementation.
package pubsub

import (
	"strconv"
	gosync "sync"
	"sync/atomic"
	time "time"

	"go.uber.org/zap"

	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/server/logging"
)

var id loggerID

type loggerID int32

func (c *loggerID) next() string {
	next := atomic.AddInt32((*int32)(c), 1)
	return "p" + strconv.Itoa(int(next))
}

// DocPublisher is a publisher that publishes events in batch.
type DocPublisher struct {
	logger             *zap.SugaredLogger
	mutex              gosync.Mutex
	events             []events.DocEvent
	docChangedCountMap map[string]int

	window    time.Duration
	closeChan chan struct{}
	subs      *DocSubscriptions
}

// NewDocPublisher creates a new instance of DocPublisher.
func NewDocPublisher(subs *DocSubscriptions, window time.Duration) *DocPublisher {
	bp := &DocPublisher{
		logger:             logging.New(id.next()),
		docChangedCountMap: make(map[string]int),
		window:             window,
		closeChan:          make(chan struct{}),
		subs:               subs,
	}

	go bp.processLoop()
	return bp
}

// Publish adds the given event to the batch. If the batch is full, it publishes
// the batch.
func (bp *DocPublisher) Publish(event events.DocEvent) {
	bp.mutex.Lock()
	defer bp.mutex.Unlock()

	// NOTE(hackerwins): If the queue contains multiple DocChangedEvents from
	// the same publisher, only the two events are processed.
	// This occurs when a client attaches/detaches a document since the order
	// of Changed and Watch/Unwatch events is not guaranteed.
	if event.Type == events.DocChanged {
		count, exists := bp.docChangedCountMap[event.Publisher.String()]
		if exists && count > 1 {
			return
		}
		bp.docChangedCountMap[event.Publisher.String()] = count + 1
	}

	bp.events = append(bp.events, event)
}

func (bp *DocPublisher) processLoop() {
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

func (bp *DocPublisher) publish() {
	bp.mutex.Lock()

	if len(bp.events) == 0 {
		bp.mutex.Unlock()
		return
	}

	events := bp.events
	bp.events = nil
	bp.docChangedCountMap = make(map[string]int)

	bp.mutex.Unlock()

	if logging.Enabled(zap.DebugLevel) {
		bp.logger.Infof(
			"Publishing batch of %d events for document %s",
			len(bp.events),
			bp.subs.docKey,
		)
	}

	for _, sub := range bp.subs.Values() {
		for _, event := range events {
			if sub.Subscriber().Compare(event.Publisher) == 0 {
				continue
			}

			if ok := sub.Publish(event); !ok {
				bp.logger.Infof(
					"Publish(%s,%s) to %s timeout or closed",
					event.Type,
					event.Publisher,
					sub.Subscriber(),
				)
			}
		}
	}
}

// Close stops the batch publisher
func (bp *DocPublisher) Close() {
	close(bp.closeChan)
}
