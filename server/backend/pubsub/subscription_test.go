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

func TestSubscriptionSelfPrune(t *testing.T) {
	actor, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)

	t.Run("publish success resets failure count", func(t *testing.T) {
		sub := NewSubscription[int](actor, 1)
		sub.maxFailures = 3

		assert.True(t, sub.Publish(1))
		assert.False(t, sub.Publish(2)) // buffer full, timeout, failureCount=1

		<-sub.Events() // drain
		assert.True(t, sub.Publish(3))
		assert.False(t, sub.IsDead())
	})

	t.Run("dead after consecutive failures", func(t *testing.T) {
		sub := NewSubscription[int](actor, 1)
		sub.maxFailures = 3

		assert.True(t, sub.Publish(1))
		for range sub.maxFailures {
			assert.False(t, sub.Publish(99))
		}
		assert.True(t, sub.IsDead())
	})

	t.Run("dead publish short-circuits without waiting timeout", func(t *testing.T) {
		sub := NewSubscription[int](actor, 1)
		sub.Close()

		start := gotime.Now()
		assert.False(t, sub.Publish(42))
		assert.Less(t, gotime.Since(start), publishTimeout/2)
	})
}
