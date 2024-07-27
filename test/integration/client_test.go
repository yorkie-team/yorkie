//go:build integration

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
	"sync"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestClient(t *testing.T) {
	t.Run("dial and close test", func(t *testing.T) {
		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)

		defer func() {
			err := cli.Close()
			assert.NoError(t, err)
		}()
	})

	t.Run("activate/deactivate test", func(t *testing.T) {
		cli, err := client.Dial(defaultServer.RPCAddr())
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

	t.Run("sync option with multiple clients test", func(t *testing.T) {
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)
		c1, c2, c3 := clients[0], clients[1], clients[2]

		ctx := context.Background()

		// 01. c1, c2, c3 attach to the same document.
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, d3))

		// 02. c1, c2 sync with push-pull mode.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("c1", 0)
			return nil
		}))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("c2", 0)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, d1.Marshal(), d2.Marshal())

		// 03. c1 and c2 sync with push-only mode. So, the changes of c1 and c2
		// are not reflected to each other.
		// But, c3 can get the changes of c1 and c2, because c3 sync with pull-pull mode.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("c1", 1)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetInteger("c2", 1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(d1.Key()).WithPushOnly()))
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key()).WithPushOnly()))
		assert.NoError(t, c1.Sync(ctx), client.WithDocKey(d1.Key()).WithPushOnly())
		assert.NoError(t, c3.Sync(ctx))
		assert.NotEqual(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, d1.Root().Get("c1").Marshal(), d3.Root().Get("c1").Marshal())
		assert.Equal(t, d2.Root().Get("c2").Marshal(), d3.Root().Get("c2").Marshal())

		// 04. c1 and c2 sync with push-pull mode.
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(d1.Key())))
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key())))
		assert.Equal(t, d1.Marshal(), d2.Marshal())
		assert.Equal(t, d1.Marshal(), d3.Marshal())
	})

	t.Run("sync option with mixed mode test", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		cli := clients[0]

		// 01. cli attach to the same document having counter.
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))

		// 02. cli update the document with creating a counter
		//     and sync with push-pull mode: CP(1, 1) -> CP(2, 2)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", crdt.IntegerCnt, 0)
			return nil
		}))
		assert.Equal(t, change.Checkpoint{ClientSeq: 1, ServerSeq: 1}, doc.Checkpoint())
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key())))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 2, ServerSeq: 2})

		// 03. cli update the document with increasing the counter(0 -> 1)
		//     and sync with push-only mode: CP(2, 2) -> CP(3, 2)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.Len(t, doc.CreateChangePack().Changes, 1)
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key()).WithPushOnly()))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 3, ServerSeq: 2})

		// 04. cli update the document with increasing the counter(1 -> 2)
		//     and sync with push-pull mode. CP(3, 2) -> CP(4, 4)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))

		// The previous increase(0 -> 1) is already pushed to the server,
		// so the ChangePack of the request only has the increase(1 -> 2).
		assert.Len(t, doc.CreateChangePack().Changes, 1)
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key())))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 4, ServerSeq: 4})
		assert.Equal(t, "2", doc.Root().GetCounter("counter").Marshal())
	})

	t.Run("deactivated client's stream test", func(t *testing.T) {
		ctx := context.Background()

		c1, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, c1.Activate(ctx))

		d1 := document.New(helper.TestDocKey(t))

		// 01. Attach the document and subscribe.
		assert.NoError(t, c1.Attach(ctx, d1))

		// 02. Deactivate the client and try to watch.
		assert.NoError(t, defaultServer.DeactivateClient(ctx, c1))

		wg := sync.WaitGroup{}
		wg.Add(1)
		stream, _ := c1.Watch(ctx, d1)

		go func() {
			defer wg.Done()

			stream.Receive()
			if err = stream.Err(); err != nil {
				assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(err))
				return
			}
		}()

		wg.Wait()
	})
}
