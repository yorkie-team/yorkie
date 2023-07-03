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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPeerAwareness(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("WatchStarted and PeersChanged event test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1, false)) }()
		assert.NoError(t, c2.Attach(ctx, d2))
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

					if wr.Type == client.PeersChanged {
						peers := wr.PeersMapByDoc[d1.Key()]
						responsePairs = append(responsePairs, watchResponsePair{
							Type:  wr.Type,
							Peers: peers,
						})

						if len(peers) == 1 {
							return
						}
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): c1.Presence(),
				c2.ID().String(): c2.Presence(),
			},
		})
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client updates its presence
		assert.NoError(t, c2.UpdatePresence(ctx, "updated", "true"))
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): c1.Presence(),
				c2.ID().String(): c2.Presence(),
			},
		})

		// 03. PeersChanged is triggered when another client closes the watch
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): c1.Presence(),
			},
		})
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})

	t.Run("Watch multiple documents test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		d3 := document.New(key.Key(helper.TestDocKey(t) + "2"))
		assert.NoError(t, c1.Attach(ctx, d1))
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

					if wr.Type == client.PeersChanged {
						peers := wr.PeersMapByDoc[d1.Key()]
						responsePairs = append(responsePairs, watchResponsePair{
							Type:  wr.Type,
							Peers: peers,
						})

						if len(peers) == 1 {
							return
						}
					}
				}
			}
		}()

		// 01. PeersChanged is triggered when another client watches the document
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): c1.Presence(),
				c2.ID().String(): c2.Presence(),
			},
		})
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2, false)) }()
		assert.NoError(t, c2.Attach(ctx, d3))
		defer func() { assert.NoError(t, c2.Detach(ctx, d3, false)) }()

		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		watch3Ctx, cancel3 := context.WithCancel(ctx)
		_, err = c2.Watch(watch3Ctx, d3)
		assert.NoError(t, err)

		// 02. PeersChanged is triggered when another client closes the watch
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): c1.Presence(),
			},
		})
		cancel2()
		cancel3()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})
}
