//go:build integration

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPeerAwareness(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("PeersChanged event test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1, err := c1.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		var expected []peersChangedPair
		var peersChangedEvents []peersChangedPair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(2)

		// starting to watch a document
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		wrch, err := c1.Watch(watch1Ctx, d1)
		assert.NoError(t, err)

		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					responsePairs = append(responsePairs, watchResponsePair{
						Type:   wr.Type,
						DocKey: wr.Key,
					})

					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()

		ch := d1.Watch()
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case e := <-ch:
					peersChangedEvents = append(peersChangedEvents, peersChangedPair{
						Type: e.Type,
						Peer: e.Publisher,
					})

					if len(peersChangedEvents) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, peersChangedPair{
			Type: document.WatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.UpdatePresence("updated", "true")
		expected = append(expected, peersChangedPair{
			Type: document.PresenceChangedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(docKey)))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(docKey)))
		assert.Equal(t, map[string]presence.Presence{
			c1.ID().String(): d1.Presence(),
			c2.ID().String(): d2.Presence(),
		}, d1.PeersMap())

		// 03. PeersChanged is triggered when another client closes the watch
		expected = append(expected, peersChangedPair{
			Type: document.UnwatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, peersChangedEvents)
		assert.Equal(t, []watchResponsePair{
			{
				Type:   client.DocumentsChanged,
				DocKey: docKey,
			},
		}, responsePairs)
	})

	t.Run("initial presence test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		c1Presence := map[string]string{
			"name":  "cli1",
			"color": "red",
		}
		c2Presence := map[string]string{
			"name":  "cli2",
			"color": "green",
		}
		d1, err := c1.Attach(ctx, docKey, c1Presence)
		assert.NoError(t, err)
		assert.Equal(t, c1Presence, d1.Presence())
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2, err := c2.Attach(ctx, docKey, c2Presence)
		assert.NoError(t, err)
		assert.Equal(t, c2Presence, d2.Presence())

		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		var expected []peersChangedPair
		var peersChangedEvents []peersChangedPair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(2)

		// starting to watch a document
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		wrch, err := c1.Watch(watch1Ctx, d1)
		assert.NoError(t, err)

		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					responsePairs = append(responsePairs, watchResponsePair{
						Type:   wr.Type,
						DocKey: wr.Key,
					})

					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()

		ch := d1.Watch()
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case e := <-ch:
					peersChangedEvents = append(peersChangedEvents, peersChangedPair{
						Type: e.Type,
						Peer: e.Publisher,
					})

					if len(peersChangedEvents) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, peersChangedPair{
			Type: document.WatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.UpdatePresence("color", "blue")
		c2Presence = map[string]string{
			"name":  "cli2",
			"color": "blue",
		}
		expected = append(expected, peersChangedPair{
			Type: document.PresenceChangedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): c2Presence,
			},
		})
		assert.Equal(t, c2Presence, d2.Presence())
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(docKey)))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(docKey)))
		assert.Equal(t, map[string]presence.Presence{
			c1.ID().String(): c1Presence,
			c2.ID().String(): c2Presence,
		}, d1.PeersMap())

		// 03. PeersChanged is triggered when another client closes the watch
		expected = append(expected, peersChangedPair{
			Type: document.UnwatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, peersChangedEvents)
		assert.Equal(t, []watchResponsePair{
			{
				Type:   client.DocumentsChanged,
				DocKey: docKey,
			},
		}, responsePairs)
	})

	t.Run("Watch multiple documents test", func(t *testing.T) {
		ctx := context.Background()

		d1, err := c1.Attach(ctx, helper.TestDocKey(t), map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()

		var expected []peersChangedPair
		var peersChangedEvents []peersChangedPair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(2)

		// starting to watch a document
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		wrch, err := c1.Watch(watch1Ctx, d1)
		assert.NoError(t, err)

		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					responsePairs = append(responsePairs, watchResponsePair{
						Type:   wr.Type,
						DocKey: wr.Key,
					})

					if wr.Type == client.DocumentsChanged {
						err := c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t)))
						assert.NoError(t, err)
					}
					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()
		ch := d1.Watch()
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case e := <-ch:
					peersChangedEvents = append(peersChangedEvents, peersChangedPair{
						Type: e.Type,
						Peer: e.Publisher,
					})

					if len(peersChangedEvents) == 2 {
						return
					}
				}
			}
		}()

		d2, err := c2.Attach(ctx, helper.TestDocKey(t), map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()
		d3, err := c2.Attach(ctx, helper.TestDocKey(t)+"2", map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d3)) }()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, peersChangedPair{
			Type: document.WatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)
		watch3Ctx, cancel3 := context.WithCancel(ctx)
		_, err = c2.Watch(watch3Ctx, d3)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client closes the watch
		expected = append(expected, peersChangedPair{
			Type: document.UnwatchedEvent,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()
		cancel3()

		wgEvents.Wait()

		assert.Equal(t, expected, peersChangedEvents)
	})
}
