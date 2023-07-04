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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document/json"
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
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.Watched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.UpdatePresence("updated", "true")
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
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
		expected = append(expected, watchResponsePair{
			Type: client.Unwatched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("initial presence test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		c1Presence := presence.Presence{
			"name":  "cli1",
			"color": "red",
		}
		c2Presence := presence.Presence{
			"name":  "cli2",
			"color": "green",
		}
		d1, err := c1.Attach(ctx, docKey, c1Presence)
		assert.NoError(t, err)
		assert.Equal(t, c1Presence, d1.Presence())
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		d2, err := c2.Attach(ctx, docKey, c2Presence)
		assert.NoError(t, err)
		assert.Equal(t, c2Presence, d2.Presence())

		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.Watched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.UpdatePresence("color", "blue")
		c2Presence = presence.Presence{
			"name":  "cli2",
			"color": "blue",
		}
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
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
		expected = append(expected, watchResponsePair{
			Type: client.Unwatched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("Watch multiple documents test", func(t *testing.T) {
		ctx := context.Background()

		d1, err := c1.Attach(ctx, helper.TestDocKey(t), map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()

		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type == client.DocumentChanged {
						err := c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t)))
						assert.NoError(t, err)
					} else {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 2 {
						return
					}
				}
			}
		}()

		d2, err := c2.Attach(ctx, helper.TestDocKey(t), map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()
		d3, err := c2.Attach(ctx, helper.TestDocKey(t)+"2", map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d3, false)) }()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.Watched,
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
		expected = append(expected, watchResponsePair{
			Type: client.Unwatched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()
		cancel3()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("allows updatePresence to be called within update", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1, err := c1.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.Watched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.Update(func(root *json.Object) error {
			root.SetString("key", "value")
			d2.UpdatePresence("updated", "true")
			return nil
		})
		assert.Equal(t, presence.Presence{"updated": "true"}, d2.Presence())
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
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
		expected = append(expected, watchResponsePair{
			Type: client.Unwatched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("receive single change in nested updates", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1, err := c1.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.Watched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		d2.Update(func(root *json.Object) error {
			root.SetString("key1", "value1")
			d2.UpdatePresence("presence", "value1")
			d2.Update(func(root *json.Object) error {
				root.SetString("key2", "value2")
				d2.UpdatePresence("presence", "value2")
				d2.Update(func(root *json.Object) error {
					root.SetString("key3", "value3")
					d2.UpdatePresence("another-presence", "value")
					return nil
				})
				return nil
			})
			return nil
		})
		assert.Equal(t, presence.Presence{"another-presence": "value", "presence": "value2"}, d2.Presence())
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
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
		expected = append(expected, watchResponsePair{
			Type: client.Unwatched,
			Peer: map[string]presence.Presence{
				c2.ID().String(): d2.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("build presence from snapshot test", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1, err := c1.Attach(ctx, docKey, map[string]string{"presence1": "value1", "presence2": "value2"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()

		// starting to watch a document
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		_, err = c1.Watch(watch1Ctx, d1)
		assert.NoError(t, err)

		// 01. Update presence over snapshot threshold.
		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			d1.UpdatePresence("presence2", fmt.Sprintf("%d", i))
			assert.NoError(t, err)
		}
		assert.Equal(t, presence.Presence{"presence1": "value1", "presence2": fmt.Sprintf("%d", helper.SnapshotThreshold)}, d1.Presence())
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// 02. c2 attaches to the document and get the presence from the snapshot.
		//     c2 doesn't know the presence from c1 because it's not watched yet.
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		assert.Equal(t, map[string]presence.Presence{c2.ID().String(): d2.Presence()}, d2.PeersMap())

		// 03. c2 watches the document and then knows about c1 presence.
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		defer cancel2()
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		assert.Equal(t, map[string]presence.Presence{c1.ID().String(): d1.Presence(), c2.ID().String(): d2.Presence()}, d2.PeersMap())
	})

	t.Run("can get peer's presence", func(t *testing.T) {
		ctx := context.Background()
		docKey := helper.TestDocKey(t)
		d1, err := c1.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		d2, err := c2.Attach(ctx, docKey, map[string]string{})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()

		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

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
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type: wr.Type,
							Peer: wr.Peer,
						})
					}
					if len(responsePairs) == 1 { // watched
						return
					}
				}
			}
		}()

		// 01. c2 doesn't know about c1 presence because it's not watched yet.
		assert.Equal(t, map[string]presence.Presence{c2.ID().String(): d2.Presence()}, d2.PeersMap())
		assert.Nil(t, d2.PeerPresence(c1.ID().String()))

		// 02. c2 knows about c1 presence because it's watched now.
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		defer cancel2()
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		assert.Equal(t, presence.Presence{}, d1.Presence())
		assert.Equal(t, map[string]presence.Presence{c1.ID().String(): d1.Presence(), c2.ID().String(): d2.Presence()}, d2.PeersMap())
		assert.Equal(t, d1.Presence(), d2.PeerPresence(c1.ID().String()))

		// 03. After c1 updates the presence, c2 can detect the changed presence.
		d1.UpdatePresence("updated", "true")
		assert.Equal(t, presence.Presence{"updated": "true"}, d1.Presence())
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(docKey)))
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(docKey)))
		assert.Equal(t, map[string]presence.Presence{c1.ID().String(): d1.Presence(), c2.ID().String(): d2.Presence()}, d2.PeersMap())
		assert.Equal(t, d1.Presence(), d2.PeerPresence(c1.ID().String()))

		wgEvents.Wait()
	})
}
