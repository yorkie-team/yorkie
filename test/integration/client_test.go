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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
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
		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetInteger("c1", 0)
			return nil
		}))
		assert.NoError(t, d1.Update(func(root *json.Object) error {
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
		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetInteger("c1", 1)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object) error {
			root.SetInteger("c2", 1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(d1.Key()).WithPushOnly()))
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(d2.Key()).WithPushOnly()))
		assert.NoError(t, c1.Sync(ctx), client.WithDocKey(d1.Key()).WithPushOnly())
		assert.NoError(t, c3.Sync(ctx))
		assert.NotEqual(t, d1.Marshal(), d2.Marshal())
		doc1Root, _ := d1.Root()
		doc1RootC1 := doc1Root.Get("c1")
		doc2Root, _ := d2.Root()
		doc2RootC2 := doc2Root.Get("c2")
		doc3Root, _ := d3.Root()
		doc3RootC1 := doc3Root.Get("c1")
		doc3RootC2 := doc3Root.Get("c2")
		assert.Equal(t, doc1RootC1.Marshal(), doc3RootC1.Marshal())
		assert.Equal(t, doc2RootC2.Marshal(), doc3RootC2.Marshal())

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
		//     and sync with push-pull mode: CP(0, 0) -> CP(1, 1)
		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.SetNewCounter("counter", crdt.IntegerCnt, 0)
			return nil
		}))
		assert.Equal(t, change.Checkpoint{ClientSeq: 0, ServerSeq: 0}, doc.Checkpoint())
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key())))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 1, ServerSeq: 1})

		// 03. cli update the document with increasing the counter(0 -> 1)
		//     and sync with push-only mode: CP(1, 1) -> CP(2, 1)
		assert.NoError(t, doc.Update(func(root *json.Object) error {
			c := root.GetCounter("counter")
			c.Increase(1)
			return nil
		}))
		assert.Len(t, doc.CreateChangePack().Changes, 1)
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key()).WithPushOnly()))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 2, ServerSeq: 1})

		// 04. cli update the document with increasing the counter(1 -> 2)
		//     and sync with push-pull mode. CP(2, 1) -> CP(3, 3)
		assert.NoError(t, doc.Update(func(root *json.Object) error {
			c := root.GetCounter("counter")
			c.Increase(1)
			return nil
		}))

		// The previous increase(0 -> 1) is already pushed to the server,
		// so the ChangePack of the request only has the increase(1 -> 2).
		assert.Len(t, doc.CreateChangePack().Changes, 1)
		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(doc.Key())))
		assert.Equal(t, doc.Checkpoint(), change.Checkpoint{ClientSeq: 3, ServerSeq: 3})
		dr, _ := doc.Root()
		c := dr.GetCounter("counter")
		assert.Equal(t, "2", c.Marshal())
	})
}
