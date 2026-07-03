//go:build integration

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

package integration

import (
	"context"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestWatchEventDeliveryPipeline guards the delivery pipeline described in
// docs/design/doc-presence.md ("Presence Event Delivery Unification").
func TestWatchEventDeliveryPipeline(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	// A watch consumer that re-enters the document (Update acquires the
	// document mutex) on every event must not deadlock against presence
	// event producers. Before the pipeline, the stream goroutine emitted
	// into a full buffer-1 channel while holding the document mutex, the
	// forwarder waited on the response channel, and a consumer inside
	// Update waited on the mutex — a three-way cycle under peer churn.
	t.Run("consumer reentering document under peer churn test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		rch, cancel1, err := c1.WatchStream(d1)
		assert.NoError(t, err)

		consumerDone := make(chan struct{})
		go func() {
			defer close(consumerDone)
			for resp := range rch {
				if resp.Err != nil {
					return
				}
				// Reenter the document on every event to hold the document
				// mutex while further events are being produced.
				assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
					p.Set("seen", "yes")
					return nil
				}))
				if resp.Type == client.DocumentChanged {
					assert.NoError(t, c1.Sync(ctx, client.WithKey(d1.Key())))
				}
			}
		}()

		// Peer churn: repeated join, presence update, and leave produce
		// bursts of watched/unwatched/presence-changed events.
		for i := 0; i < 5; i++ {
			d2 := document.New(d1.Key())
			assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
			_, cancel2, err := c2.WatchStream(d2)
			assert.NoError(t, err)

			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				p.Set("round", "r")
				return nil
			}))
			assert.NoError(t, c2.Sync(ctx))

			cancel2()
			assert.NoError(t, c2.Detach(ctx, d2))
		}

		cancel1()
		select {
		case <-consumerDone:
		case <-gotime.After(30 * gotime.Second):
			t.Fatal("watch consumer deadlocked against presence event producers")
		}
	})
}
