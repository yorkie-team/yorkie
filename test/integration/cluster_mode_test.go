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
		agentA := helper.TestYorkie()
		agentB := helper.TestYorkie()
		assert.NoError(t, agentA.Start())
		assert.NoError(t, agentB.Start())

		defer func() {
			assert.NoError(t, agentA.Shutdown(true))
			assert.NoError(t, agentB.Shutdown(true))
		}()

		ctx := context.Background()
		clientA, err := client.Dial(agentA.RPCAddr())
		assert.NoError(t, err)
		clientB, err := client.Dial(agentB.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, clientA.Activate(ctx))
		assert.NoError(t, clientB.Activate(ctx))

		docA := document.New(helper.Collection, t.Name())
		docB := document.New(helper.Collection, t.Name())

		assert.NoError(t, clientA.Attach(ctx, docA))
		assert.NoError(t, clientB.Attach(ctx, docB))

		wg := gosync.WaitGroup{}

		wg.Add(1)
		rch := clientA.Watch(ctx, docA)
		go func() {
			defer wg.Done()

			select {
			case resp := <-rch:
				if resp.Err == io.EOF {
					return
				}
				assert.NoError(t, resp.Err)

				err := clientA.Sync(ctx, resp.Keys...)
				assert.NoError(t, err)
			case <-time.After(time.Second):
				return
			}
		}()

		err = docB.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("hello", "world")
			return nil
		})
		assert.NoError(t, err)

		wg.Wait()

		// TODO(hackerwins): uncomment below test
		// assert.Equal(t, docA.Marshal(), docB.Marshal())

		defer func() {
			assert.NoError(t, clientA.Deactivate(ctx))
			assert.NoError(t, clientA.Close())

			assert.NoError(t, clientB.Deactivate(ctx))
			assert.NoError(t, clientB.Close())
		}()
	})
}
