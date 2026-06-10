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
	"fmt"
	"sync"
	gotime "time"

	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/pkg/cmap"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

const (
	// publishTimeout is the timeout for publishing an event.
	publishTimeout = 100 * gotime.Millisecond
)

// defaultMaxConsecutivePublishFailures is the threshold of consecutive
// Publish failures (timeout or already-closed channel) after which a
// Subscription marks itself dead and lets the BatchPublisher reap it.
// Set conservatively so transient slow consumers are not pruned, while
// keeping leaked subscriptions from accumulating indefinitely.
//
// Declared as var (not const) so tests can shorten it via
// SetDefaultMaxConsecutivePublishFailures; production code keeps the
// default. Access is guarded by defaultMaxFailuresMu so the setter and
// the reader in NewSubscription stay race-free.
var (
	defaultMaxFailuresMu                 sync.RWMutex
	defaultMaxConsecutivePublishFailures = 100
)

// SetDefaultMaxConsecutivePublishFailures overrides the default failure
// threshold for newly created Subscriptions. Intended for tests only;
// production code should rely on the package default. Callers should
// restore the previous value with defer. Panics on n < 1 since a
// non-positive threshold would prune every subscription on first send.
func SetDefaultMaxConsecutivePublishFailures(n int) (previous int) {
	if n < 1 {
		panic("pubsub: max consecutive publish failures must be >= 1")
	}
	defaultMaxFailuresMu.Lock()
	defer defaultMaxFailuresMu.Unlock()
	previous = defaultMaxConsecutivePublishFailures
	defaultMaxConsecutivePublishFailures = n
	return previous
}

func currentDefaultMaxFailures() int {
	defaultMaxFailuresMu.RLock()
	defer defaultMaxFailuresMu.RUnlock()
	return defaultMaxConsecutivePublishFailures
}

// Subscription represents a subscription of a subscriber to events of type E.
type Subscription[E any] struct {
	id           string
	subscriber   time.ActorID
	mu           sync.Mutex
	closed       bool
	failureCount int
	maxFailures  int
	events       chan E
}

// NewSubscription creates a new instance of Subscription with the given buffer size.
func NewSubscription[E any](subscriber time.ActorID, bufSize int) *Subscription[E] {
	return &Subscription[E]{
		id:          xid.New().String(),
		subscriber:  subscriber,
		events:      make(chan E, bufSize),
		closed:      false,
		maxFailures: currentDefaultMaxFailures(),
	}
}

// ID returns the id of this subscription.
func (s *Subscription[E]) ID() string {
	return s.id
}

// Events returns the event channel of this subscription.
func (s *Subscription[E]) Events() chan E {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription[E]) Subscriber() time.ActorID {
	return s.subscriber
}

// Close closes all resources of this Subscription.
func (s *Subscription[E]) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.closed {
		s.closed = true
		close(s.events)
	}
}

// Publish publishes the given event to the subscriber.
//
// On a successful send the internal failure counter is reset. On timeout
// or an already-closed channel the counter increments; once it reaches
// maxFailures the subscription closes itself so the BatchPublisher can
// reap it on the next iteration. This is the only fallback path when
// the owning stream handler never invokes Unsubscribe.
func (s *Subscription[E]) Publish(event E) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return false
	}

	// NOTE(hackerwins): When a subscription is being closed by a subscriber,
	// the subscriber may not receive messages.
	select {
	case s.events <- event:
		s.failureCount = 0
		return true
	case <-gotime.After(publishTimeout):
		s.failureCount++
		if s.failureCount >= s.maxFailures {
			s.closed = true
			close(s.events)
		}
		return false
	}
}

// IsDead reports whether this Subscription has been closed, either
// explicitly via Close or by self-prune after too many consecutive
// Publish failures.
func (s *Subscription[E]) IsDead() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Subscriptions is a collection of Subscription[E] with an associated BatchPublisher.
type Subscriptions[E any] struct {
	name        string
	internalMap *cmap.Map[string, *Subscription[E]]
	publisher   *BatchPublisher[E]
}

// NewSubscriptions creates a new Subscriptions collection.
func NewSubscriptions[E any](
	name string,
	publisherFactory func(subs *Subscriptions[E]) *BatchPublisher[E],
) *Subscriptions[E] {
	s := &Subscriptions[E]{
		name:        name,
		internalMap: cmap.New[string, *Subscription[E]](),
	}
	s.publisher = publisherFactory(s)
	return s
}

// Name returns the display name of this subscriptions collection (used for logging).
func (s *Subscriptions[E]) Name() string {
	return s.name
}

// Set adds the given subscription.
func (s *Subscriptions[E]) Set(sub *Subscription[E]) {
	s.internalMap.Set(sub.ID(), sub)
}

// Values returns the values of these subscriptions.
func (s *Subscriptions[E]) Values() []*Subscription[E] {
	return s.internalMap.Values()
}

// Publish publishes the given event.
func (s *Subscriptions[E]) Publish(event E) {
	s.publisher.Publish(event)
}

// Delete deletes the subscription of the given id.
func (s *Subscriptions[E]) Delete(id string) {
	s.internalMap.Delete(id, func(sub *Subscription[E], exists bool) bool {
		if exists {
			sub.Close()
		}
		return exists
	})
}

// Len returns the length of these subscriptions.
func (s *Subscriptions[E]) Len() int {
	return s.internalMap.Len()
}

// Close closes the subscriptions.
func (s *Subscriptions[E]) Close() {
	s.publisher.Close()
}

// String returns a string representation of this subscriptions collection.
func (s *Subscriptions[E]) String() string {
	return fmt.Sprintf("Subscriptions(%s)", s.name)
}
