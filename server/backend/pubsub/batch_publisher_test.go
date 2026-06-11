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
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestBatchPublisherReapsDeadSubscriptions(t *testing.T) {
	actor, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)

	subs := NewSubscriptions(
		"test",
		func(s *Subscriptions[int]) *BatchPublisher[int] {
			return NewBatchPublisher(s, 50*gotime.Millisecond, BatchPublisherConfig[int]{})
		},
	)
	defer subs.Close()

	sub := NewSubscription[int](actor, 1)
	sub.maxFailures = 2
	subs.Set(sub)
	assert.Equal(t, 1, subs.Len())

	// Saturate the subscription's buffer so subsequent sends time out.
	assert.True(t, sub.Publish(1))

	// Enqueue events through the publisher so its publish loop sees them.
	for i := range 4 {
		subs.Publish(i)
	}

	// Each failing send blocks for publishTimeout. With maxFailures=2 and
	// buffer-full state, the second send marks the sub dead. We wait long
	// enough for the publish loop to reach the dead branch and reap.
	assert.Eventually(t, func() bool {
		return sub.IsDead() && subs.Len() == 0
	}, 3*gotime.Second, 50*gotime.Millisecond,
		"subscription should self-prune and be reaped by BatchPublisher")
}
