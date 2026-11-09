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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/types/events"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestChannelSubscriptionsDelivery(t *testing.T) {
	publisher, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1})
	assert.NoError(t, err)
	watcher, err := time.ActorIDFromBytes([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2})
	assert.NoError(t, err)
	refKey := types.ChannelRefKey{ProjectID: types.ID("000000000000000000000000"), ChannelKey: "room"}
	broadcast := func(n int) events.ChannelEvent {
		return events.ChannelEvent{
			Type:      events.ChannelBroadcast,
			Key:       refKey,
			Publisher: publisher,
			Topic:     "t",
			Payload:   []byte{byte(n)},
		}
	}

	// drain returns the payloads delivered to sub so far without waiting.
	drain := func(sub *ChannelSubscription) []byte {
		var got []byte
		for {
			select {
			case e := <-sub.Events():
				got = append(got, e.Payload...)
			default:
				return got
			}
		}
	}

	t.Run("an event on an idle channel is delivered without waiting for the window", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 100*gotime.Millisecond)
			defer subs.Close()
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			gotime.Sleep(30 * gotime.Millisecond)
			subs.Publish(broadcast(1))
			synctest.Wait()

			assert.Equal(t, []byte{1}, drain(sub))
		})
	})

	t.Run("a burst is delivered at most once per window", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 100*gotime.Millisecond)
			defer subs.Close()
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			subs.Publish(broadcast(1))
			synctest.Wait()
			assert.Equal(t, []byte{1}, drain(sub), "the first event goes out at once")

			gotime.Sleep(10 * gotime.Millisecond)
			subs.Publish(broadcast(2))
			gotime.Sleep(10 * gotime.Millisecond)
			subs.Publish(broadcast(3))
			synctest.Wait()
			assert.Empty(t, drain(sub), "events within the window wait for it")

			gotime.Sleep(80 * gotime.Millisecond)
			synctest.Wait()
			assert.Equal(t, []byte{2, 3}, drain(sub), "the window's end delivers them together")

			gotime.Sleep(150 * gotime.Millisecond)
			subs.Publish(broadcast(4))
			synctest.Wait()
			assert.Equal(t, []byte{4}, drain(sub), "an idle channel again delivers at once")
		})
	})

	t.Run("an event right after the timer's publish goes out a window later", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 100*gotime.Millisecond)
			defer subs.Close()
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			subs.Publish(broadcast(1))
			synctest.Wait()
			subs.Publish(broadcast(2))
			gotime.Sleep(100 * gotime.Millisecond)
			synctest.Wait()
			assert.Equal(t, []byte{1, 2}, drain(sub))

			subs.Publish(broadcast(3))
			synctest.Wait()

			gotime.Sleep(99 * gotime.Millisecond)
			synctest.Wait()
			assert.Empty(t, drain(sub), "3 waits for the window after the timer's publish")
			gotime.Sleep(1 * gotime.Millisecond)
			synctest.Wait()
			assert.Equal(t, []byte{3}, drain(sub))
		})
	})

	t.Run("Close delivers what waits for an armed timer", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 100*gotime.Millisecond)
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			subs.Publish(broadcast(1))
			synctest.Wait()
			subs.Publish(broadcast(2))
			synctest.Wait()
			assert.Equal(t, []byte{1}, drain(sub))

			subs.Close()
			synctest.Wait()
			assert.Equal(t, []byte{2}, drain(sub))
		})
	})

	t.Run("a window of zero delivers every event at once", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 0)
			defer subs.Close()
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			for i := range 3 {
				subs.Publish(broadcast(i))
				synctest.Wait()
				assert.Equal(t, []byte{byte(i)}, drain(sub))
			}
		})
	})

	t.Run("SetWindow times the next delivery", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 100*gotime.Millisecond)
			defer subs.Close()
			sub := NewChannelSubscription(watcher)
			subs.Set(sub)

			subs.Publish(broadcast(1))
			synctest.Wait()
			assert.Equal(t, []byte{1}, drain(sub))

			subs.publisher.SetWindow(10 * gotime.Millisecond)
			subs.Publish(broadcast(2))
			gotime.Sleep(10 * gotime.Millisecond)
			synctest.Wait()
			assert.Equal(t, []byte{2}, drain(sub))
		})
	})

	t.Run("the publisher does not deliver a broadcast back to its sender", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			subs := newChannelSubscriptions(refKey, 0)
			defer subs.Close()
			self := NewChannelSubscription(publisher)
			subs.Set(self)

			subs.Publish(broadcast(1))
			synctest.Wait()
			assert.Empty(t, drain(self))
		})
	})
}
