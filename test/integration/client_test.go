// +build integration

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
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClient(t *testing.T) {
	t.Run("dial and close test", func(t *testing.T) {
		cli, err := client.Dial(testYorkie.RPCAddr())
		assert.NoError(t, err)

		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()
	})

	t.Run("activate/deactivate test", func(t *testing.T) {
		cli, err := client.Dial(testYorkie.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		ctx := context.Background()

		err = cli.Activate(ctx)
		assert.NoError(t, err)
		assert.True(t, cli.IsActive())

		// Already activated
		err = cli.Activate(ctx)
		assert.NoError(t, err)
		assert.True(t, cli.IsActive())

		err = cli.Deactivate(ctx)
		assert.NoError(t, err)
		assert.False(t, cli.IsActive())

		// Already deactivated
		err = cli.Deactivate(ctx)
		assert.NoError(t, err)
		assert.False(t, cli.IsActive())
	})

	t.Run("update metadata test", func(t *testing.T) {
		cli, err := client.Dial(testYorkie.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()

		assert.Equal(t, make(map[string]string), cli.Metadata())

		metadata := map[string]string{
			"name": "yorkie",
		}
		ctx := context.Background()
		cli.UpdateMetadata(ctx, metadata)
		assert.Equal(t, metadata, cli.Metadata())
	})

	t.Run("update metadata watching test", func(t *testing.T) {
		ctx := context.Background()

		clients := createActivatedClients(t, 2)
		c1 := clients[0]
		c2 := clients[1]
		defer func() {
			cleanupClients(t, clients)
		}()

		doc := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, doc)
		assert.NoError(t, err)

		err = c2.Attach(ctx, doc)
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		rch := c1.Watch(ctx, doc)

		metadata := map[string]string{
			"name": "Yorkie",
		}

		go func() {
			for {
				select {
				case <-ctx.Done():
					assert.Fail(t, "unexpected ctx done")
				case resp := <-rch:
					if resp.Err == io.EOF || status.Code(resp.Err) == codes.Canceled {
						return
					}
					assert.NoError(t, resp.Err)

					if resp.EventType == types.ClientChangedEvent {
						assert.Equal(t, metadata, resp.Publisher.Metadata)
						wg.Done()
					}
				}
			}
		}()

		c2.Watch(ctx, doc)

		wg.Add(1)
		err = c2.UpdateMetadata(ctx, metadata)
		assert.NoError(t, err)

		wg.Wait()
	})
}
