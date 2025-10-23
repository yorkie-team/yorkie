//go:build integration

/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestBroadcast(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("broadcast to subscribers except publisher test", func(t *testing.T) {
		bch := make(chan string)
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

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		rch1, _, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		rch2, _, err := c2.WatchStream(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, d3, client.WithRealtimeSync()))
		rch3, _, err := c3.WatchStream(d3)
		assert.NoError(t, err)
		d3.SubscribeBroadcastEvent("mention", handler)

		err = d3.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case <-rch3:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber,
					// except the publisher, successfully receives the event.
					assert.Equal(t, 2, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("no broadcasts to unsubscribers", func(t *testing.T) {
		bch := make(chan string)
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

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		rch1, _, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		rch2, _, err := c2.WatchStream(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		// d1 unsubscribes to the broadcast event.
		d1.UnsubscribeBroadcastEvent("mention")

		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if both the unsubscriber
					// and the publisher don't receive the event.
					assert.Equal(t, 0, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("unsubscriber can broadcast", func(t *testing.T) {
		bch := make(chan string)
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

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		rch1, _, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		// c2 doesn't subscribe to the "mention" broadcast event.
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		rch2, _, err := c2.WatchStream(d2)
		assert.NoError(t, err)

		// The unsubscriber c2 broadcasts the "mention" event.
		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber
					// receives the unsubscriber's event.
					assert.Equal(t, 1, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("reject to broadcast unserializable payload", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		_, _, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", nil)

		// Try to broadcast an unserializable payload.
		ch := make(chan string)
		err = d1.Broadcast("mention", ch)
		assert.ErrorIs(t, document.ErrUnsupportedPayloadType, err)
	})

	t.Run("error occurs while handling broadcast event", func(t *testing.T) {
		var ErrBroadcastEventHandlingError = errors.New("")

		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			return ErrBroadcastEventHandlingError
		}

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		rch1, _, err := c1.WatchStream(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		rch2, _, err := c2.WatchStream(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case resp := <-rch1:
					if resp.Err != nil {
						assert.Equal(t, resp.Type, client.DocumentBroadcast)
						assert.ErrorIs(t, resp.Err, ErrBroadcastEventHandlingError)
						rcv++
					}
				case <-rch2:
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber
					// successfully receives the event.
					assert.Equal(t, 1, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})
}
