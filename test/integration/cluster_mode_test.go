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
		client1, err := client.Dial(agent1.RPCAddr(), client.Option{Metadata: map[string]string{
			"name": "client1",
		}})
		assert.NoError(t, err)
		client2, err := client.Dial(agent2.RPCAddr(), client.Option{Metadata: map[string]string{
			"name": "client2",
		}})
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

		var responsePairs []watchResponsePair

		// watch the first document
		wg := gosync.WaitGroup{}
		wg.Add(1)
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		wrch, err := client1.Watch(watch1Ctx, doc1)
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
						peers := wr.PeersMapByDoc[doc1.Key().BSONKey()]
						responsePairs = append(responsePairs, watchResponsePair{Type: wr.Type, peers: peers})
					} else if wr.Type == client.DocumentsChanged {
						assert.NoError(t, client1.Sync(ctx, wr.Keys...))
					}
				case <-time.After(time.Second):
					return
				}
			}
		}()

		// watch the second document
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = client2.Watch(watch2Ctx, doc2)
		assert.NoError(t, err)

		// closes the second watch
		cancel2()

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("hello", "world")
			return nil
		})
		assert.NoError(t, err)

		err = client2.Sync(ctx)
		assert.NoError(t, err)

		wg.Wait()

		assert.Equal(t, doc1.Marshal(), doc2.Marshal())
		assert.Equal(t, responsePairs, []watchResponsePair{{
			Type:  client.PeersChanged,
			peers: map[string]client.Metadata{
				client1.ID().String(): client1.Metadata(),
				client2.ID().String(): client2.Metadata(),
			},
		}, {
			Type:  client.PeersChanged,
			peers: map[string]client.Metadata{
				client1.ID().String(): client1.Metadata(),
			},
		}})
	})
}
