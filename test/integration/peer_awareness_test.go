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
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPeerAwareness(t *testing.T) {
	t.Run("peer awareness test", func(t *testing.T) {
		agentA := helper.TestYorkie()
		agentB := helper.TestYorkie()
		assert.NoError(t, agentA.Start())
		assert.NoError(t, agentB.Start())
		defer func() {
			assert.NoError(t, agentA.Shutdown(true))
			assert.NoError(t, agentB.Shutdown(true))
		}()

		ctx := context.Background()
		clientA, err := client.Dial(agentA.RPCAddr(), client.Option{Metadata: map[string]string{
			"agent": agentA.RPCAddr(),
		}})
		assert.NoError(t, err)
		clientB, err := client.Dial(agentB.RPCAddr(), client.Option{Metadata: map[string]string{
			"agent": agentB.RPCAddr(),
		}})
		assert.NoError(t, err)
		assert.NoError(t, clientA.Activate(ctx))
		assert.NoError(t, clientB.Activate(ctx))

		docA := document.New(helper.Collection, t.Name())
		docB := document.New(helper.Collection, t.Name())

		assert.NoError(t, clientA.Attach(ctx, docA))
		assert.NoError(t, clientB.Attach(ctx, docB))

		wg := sync.WaitGroup{}
		wg.Add(1)
		rchA := clientA.Watch(ctx, docA)
		wg.Add(1)
		rchB := clientB.Watch(ctx, docB)

		go func() {
			defer wg.Done()

			select {
			case resp := <- rchA:
				if resp.Err == io.EOF {
					return
				}
				assert.NoError(t, resp.Err)
			}
		}()

		go func() {
			defer wg.Done()

			select {
			case resp := <- rchB:
				if resp.Err == io.EOF {
					return
				}
				assert.NoError(t, resp.Err)
			}
		}()

		wg.Wait()
	})
}