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
	"io"
	"sort"
	gosync "sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/server/backend/sync"
	"github.com/yorkie-team/yorkie/test/helper"
)

func keysFromServers(m map[string]*sync.ServerInfo) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	return keys
}

func withTwoClientsAndDocsInClusterMode(
	t *testing.T,
	fn func(t *testing.T, c1, c2 *client.Client, d1, d2 *document.Document),
) {
	ctx := context.Background()

	// creates two servers
	svr1 := helper.TestServer()
	svr2 := helper.TestServer()
	assert.NoError(t, svr1.Start())
	assert.NoError(t, svr2.Start())
	defer func() {
		assert.NoError(t, svr1.Shutdown(true))
		assert.NoError(t, svr2.Shutdown(true))
	}()

	// creates two clients, each connecting to the two servers
	client1, err := client.Dial(
		svr1.RPCAddr(),
		client.WithPresence(types.Presence{"name": "client1"}),
	)
	assert.NoError(t, err)
	client2, err := client.Dial(
		svr2.RPCAddr(),
		client.WithPresence(types.Presence{"name": "client2"}),
	)
	assert.NoError(t, err)
	assert.NoError(t, client1.Activate(ctx))
	assert.NoError(t, client2.Activate(ctx))
	defer func() {
		assert.NoError(t, client1.Close())
		assert.NoError(t, client2.Close())
	}()

	doc1 := document.New(key.Key(t.Name()))
	doc2 := document.New(key.Key(t.Name()))

	assert.NoError(t, client1.Attach(ctx, doc1))
	assert.NoError(t, client2.Attach(ctx, doc2))
	defer func() { assert.NoError(t, client1.Detach(ctx, doc1)) }()
	defer func() { assert.NoError(t, client2.Detach(ctx, doc2)) }()

	fn(t, client1, client2, doc1, doc2)
}

func TestClusterMode(t *testing.T) {
	t.Run("member list test", func(t *testing.T) {
		svrA := helper.TestServer()
		svrB := helper.TestServer()

		assert.NoError(t, svrA.Start())
		assert.NoError(t, svrB.Start())
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, keysFromServers(svrA.Members()), keysFromServers(svrB.Members()))
		assert.Len(t, defaultServer.Members(), 3)

		assert.NoError(t, svrA.Shutdown(true))
		time.Sleep(100 * time.Millisecond)
		assert.Len(t, defaultServer.Members(), 2)

		assert.NoError(t, svrB.Shutdown(true))
		time.Sleep(100 * time.Millisecond)
		assert.Len(t, defaultServer.Members(), 1)
	})

	t.Run("watch document across servers test", func(t *testing.T) {
		withTwoClientsAndDocsInClusterMode(t, func(
			t *testing.T,
			c1, c2 *client.Client,
			d1, d2 *document.Document,
		) {
			ctx := context.Background()

			var expected []watchResponsePair
			var responsePairs []watchResponsePair

			wg := gosync.WaitGroup{}
			wg.Add(1)

			// starting to watch a document
			watch1Ctx, cancel1 := context.WithCancel(ctx)
			defer cancel1()
			wrch, err := c1.Watch(watch1Ctx, d1)
			assert.NoError(t, err)
			go func() {
				defer wg.Done()

				for {
					select {
					case wr := <-wrch:
						if wr.Err == io.EOF {
							return
						}
						assert.NoError(t, wr.Err)

						if wr.Type == client.PeersChanged {
							peers := wr.PeersMapByDoc[d1.Key().String()]
							responsePairs = append(responsePairs, watchResponsePair{
								Type:  wr.Type,
								Peers: peers,
							})
						} else if wr.Type == client.DocumentsChanged {
							assert.NoError(t, c1.Sync(ctx, wr.Keys...))
						}
					case <-time.After(time.Second):
						return
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

			assert.NoError(t, d2.Update(func(root *proxy.ObjectProxy) error {
				root.SetString("hello", "world")
				return nil
			}))
			assert.NoError(t, c2.Sync(ctx))

			wg.Wait()

			assert.Equal(t, d1.Marshal(), d2.Marshal())
			assert.Equal(t, expected, responsePairs)
		})
	})
}
