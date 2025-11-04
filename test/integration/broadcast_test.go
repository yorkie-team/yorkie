//go:build integration

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

package integration

import (
	"context"
	gojson "encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/pkg/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPresenceBroadcast(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("broadcast to subscribers except publisher test", func(t *testing.T) {
		bch := make(chan string, 10)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			// Send the unmarshaled payload to the channel to notify that this
			// subscriber receives the event.
			bch <- mentionedBy
			return nil
		}

		presenceKey := helper.TestDocKey(t)

		p1 := presence.New(presenceKey)
		assert.NoError(t, c1.Attach(ctx, p1))
		countChan1, closeWatch1, err := c1.WatchPresence(ctx, p1)
		assert.NoError(t, err)
		defer closeWatch1()
		p1.SubscribeBroadcastEvent("mention", handler)

		p2 := presence.New(presenceKey)
		assert.NoError(t, c2.Attach(ctx, p2))
		countChan2, closeWatch2, err := c2.WatchPresence(ctx, p2)
		assert.NoError(t, err)
		defer closeWatch2()
		p2.SubscribeBroadcastEvent("mention", handler)

		p3 := presence.New(presenceKey)
		assert.NoError(t, c3.Attach(ctx, p3))
		countChan3, closeWatch3, err := c3.WatchPresence(ctx, p3)
		assert.NoError(t, err)
		defer closeWatch3()
		p3.SubscribeBroadcastEvent("mention", handler)

		// Wait for initial count updates
		<-countChan1
		<-countChan2
		<-countChan3

		err = p3.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			timeout := time.After(2 * time.Second)
			for {
				select {
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
					if rcv == 2 {
						// Assuming that every subscriber can receive the broadcast
						// event within this timeout period, check if every subscriber,
						// except the publisher, successfully receives the event.
						return
					}
				case <-timeout:
					// Publisher should not receive their own broadcast
					assert.Equal(t, 2, rcv, "Expected 2 subscribers to receive broadcast (excluding publisher)")
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("no broadcasts to unsubscribers", func(t *testing.T) {
		bch := make(chan string, 10)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			bch <- mentionedBy
			return nil
		}

		presenceKey := helper.TestDocKey(t)

		p1 := presence.New(presenceKey)
		assert.NoError(t, c1.Attach(ctx, p1))
		countChan1, closeWatch1, err := c1.WatchPresence(ctx, p1)
		assert.NoError(t, err)
		defer closeWatch1()
		p1.SubscribeBroadcastEvent("mention", handler)

		p2 := presence.New(presenceKey)
		assert.NoError(t, c2.Attach(ctx, p2))
		countChan2, closeWatch2, err := c2.WatchPresence(ctx, p2)
		assert.NoError(t, err)
		defer closeWatch2()
		p2.SubscribeBroadcastEvent("mention", handler)

		// Wait for initial count updates
		<-countChan1
		<-countChan2

		// p1 unsubscribes to the broadcast event
		p1.UnsubscribeBroadcastEvent("mention")

		err = p2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			timeout := time.After(2 * time.Second)
			for {
				select {
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-timeout:
					// Both the unsubscriber and the publisher should not receive the event
					assert.Equal(t, 0, rcv, "Unsubscriber and publisher should not receive broadcast")
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("unsubscriber can broadcast", func(t *testing.T) {
		bch := make(chan string, 10)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			bch <- mentionedBy
			return nil
		}

		presenceKey := helper.TestDocKey(t)

		p1 := presence.New(presenceKey)
		assert.NoError(t, c1.Attach(ctx, p1))
		countChan1, closeWatch1, err := c1.WatchPresence(ctx, p1)
		assert.NoError(t, err)
		defer closeWatch1()
		p1.SubscribeBroadcastEvent("mention", handler)

		// c2 doesn't subscribe to the "mention" broadcast event
		p2 := presence.New(presenceKey)
		assert.NoError(t, c2.Attach(ctx, p2))
		countChan2, closeWatch2, err := c2.WatchPresence(ctx, p2)
		assert.NoError(t, err)
		defer closeWatch2()

		// Wait for initial count updates
		<-countChan1
		<-countChan2

		// The unsubscriber c2 broadcasts the "mention" event
		err = p2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			timeout := time.After(2 * time.Second)
			for {
				select {
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
					if rcv == 1 {
						// Only the subscriber c1 should receive the broadcast
						return
					}
				case <-timeout:
					assert.Equal(t, 1, rcv, "Only the subscriber should receive broadcast")
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})
}
