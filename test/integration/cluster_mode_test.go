// +build integration

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

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie/backend/sync"
)

func keysFromAgents(m map[string]*sync.AgentInfo) []string {
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

	// creates two agents
	agent1 := helper.TestYorkie()
	agent2 := helper.TestYorkie()
	assert.NoError(t, agent1.Start())
	assert.NoError(t, agent2.Start())
	defer func() {
		assert.NoError(t, agent1.Shutdown(true))
		assert.NoError(t, agent2.Shutdown(true))
	}()

	// creates two clients, each connecting to the two agents
	client1, err := client.Dial(agent1.RPCAddr(), client.Option{
		Metadata: types.Metadata{"name": "client1"},
	})
	assert.NoError(t, err)
	client2, err := client.Dial(agent2.RPCAddr(), client.Option{
		Metadata: types.Metadata{"name": "client2"},
	})
	assert.NoError(t, err)
	assert.NoError(t, client1.Activate(ctx))
	assert.NoError(t, client2.Activate(ctx))
	defer func() {
		assert.NoError(t, client1.Close())
		assert.NoError(t, client2.Close())
	}()

	doc1 := document.New(helper.Collection, t.Name())
	doc2 := document.New(helper.Collection, t.Name())

	assert.NoError(t, client1.Attach(ctx, doc1))
	assert.NoError(t, client2.Attach(ctx, doc2))
	defer func() { assert.NoError(t, client1.Detach(ctx, doc1)) }()
	defer func() { assert.NoError(t, client2.Detach(ctx, doc2)) }()

	fn(t, client1, client2, doc1, doc2)
}

func TestClusterMode(t *testing.T) {
	t.Run("member list test", func(t *testing.T) {
		agentA := helper.TestYorkie()
		agentB := helper.TestYorkie()

		assert.NoError(t, agentA.Start())
		assert.NoError(t, agentB.Start())
		time.Sleep(100 * time.Millisecond)
		assert.Equal(t, keysFromAgents(agentA.Members()), keysFromAgents(agentB.Members()))
		assert.Len(t, defaultAgent.Members(), 3)

		assert.NoError(t, agentA.Shutdown(true))
		time.Sleep(100 * time.Millisecond)
		assert.Len(t, defaultAgent.Members(), 2)

		assert.NoError(t, agentB.Shutdown(true))
		time.Sleep(100 * time.Millisecond)
		assert.Len(t, defaultAgent.Members(), 1)
	})

	t.Run("watch document across agents test", func(t *testing.T) {
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
							peers := wr.PeersMapByDoc[d1.Key().BSONKey()]
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
				Peers: map[string]types.Metadata{
					c1.ID().String(): c1.Metadata(),
					c2.ID().String(): c2.Metadata(),
				},
			})

			watch2Ctx, cancel2 := context.WithCancel(ctx)
			_, err = c2.Watch(watch2Ctx, d2)
			assert.NoError(t, err)

			// 02. PeersChanged is triggered when another client updates it's metadata
			assert.NoError(t, c2.UpdateMetadata(ctx, "updated", "true"))
			expected = append(expected, watchResponsePair{
				Type: client.PeersChanged,
				Peers: map[string]types.Metadata{
					c1.ID().String(): c1.Metadata(),
					c2.ID().String(): c2.Metadata(),
				},
			})

			// 03. PeersChanged is triggered when another client closes the watch
			expected = append(expected, watchResponsePair{
				Type: client.PeersChanged,
				Peers: map[string]types.Metadata{
					c1.ID().String(): c1.Metadata(),
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
