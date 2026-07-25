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
	"testing/synctest"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestBatchPublisherReapsDeadSubscriptions(t *testing.T) {
	actor, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)

	synctest.Test(t, func(t *testing.T) {
		const window = 50 * gotime.Millisecond
		subs := NewSubscriptions(
			"test",
			func(s *Subscriptions[int]) *BatchPublisher[int] {
				return NewBatchPublisher(s, window, BatchPublisherConfig[int]{})
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
		// buffer-full state, the second send marks the sub dead and the publish
		// loop reaps it once the fake clock passes the window plus two timeouts.
		gotime.Sleep(window + 2*publishTimeout)
		synctest.Wait()

		assert.True(t, sub.IsDead(),
			"subscription should self-prune after consecutive publish failures")
		assert.Equal(t, 0, subs.Len(),
			"dead subscription should be reaped by BatchPublisher")
	})
}
