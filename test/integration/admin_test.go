//go:build integration

/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/cmd/yorkie/config"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAdmin(t *testing.T) {
	ctx := context.Background()

	adminAddr := "localhost" + ":21201"
	token, err := config.LoadToken(adminAddr)
	assert.NoError(t, err)

	adminCli, err := admin.Dial(adminAddr, admin.WithToken(token))
	assert.NoError(t, err)
	_, err = adminCli.LogIn(ctx, "admin", "admin")
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, adminCli.Close())
	}()

	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("admin single-client document deletion sync test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		d1 := document.New(helper.TestDocKey(t))

		// 01. client is not activated.
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrClientNotActivated)

		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.Error(t, err)

		// 02. document is not attached.
		assert.NoError(t, cli.Activate(ctx))
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)

		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.Error(t, err)

		// 03. sdk document is attached. but DB's clientDocInfo is already removed.
		assert.NoError(t, cli.Attach(ctx, d1))
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)
		assert.Equal(t, document.StatusAttached, d1.Status())

		// 04. try to update a removed document.
		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}))

		// 05. try to attach a removed document. (document status is not changed in attached)
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)

		// 06. try to sync a removed document. after sync, document status should be removed.
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, document.StatusRemoved, d1.Status())

		// 07. after sync, try to update a removed document. (document status is changed in removed)
		assert.ErrorIs(t, d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}), document.ErrDocumentRemoved)
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)
	})

	t.Run("admin removed document creation test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and removes it.
		d1 := document.New(helper.TestDocKey(t))
		err = d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))

		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)

		// 02. cli2 creates d2 with the same key.
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, c1.Sync(ctx))

		// 03. cli1 creates d3 with the same key.
		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d3))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d3}, {c2, d2}})
		assert.Equal(t, document.StatusRemoved, d1.Status())
	})

	t.Run("admin removed document pushpull test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and cli2 syncs.
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. cli1 updates d1 and removes it.
		err = d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v2")
			return nil
		})
		assert.NoError(t, err)
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)

		// 03. cli2 syncs and checks that d2 is removed.
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("admin removed document detachment test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and cli2 syncs.
		d1 := document.New(helper.TestDocKey(t))
		err = d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. cli1 removes d1 and cli2 detaches d2.
		err := adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.Error(t, c2.Detach(ctx, d2))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
	})

	t.Run("admin removed document removal test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and cli2 syncs.
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. cli1 removes d1 and cli2 removes d2.
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.Error(t, err)

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
	})

	// State transition of document
	// ┌──────────┐ Attach ┌──────────┐ Remove ┌─────────┐
	// │ Detached ├───────►│ Attached ├───────►│ Removed │
	// └──────────┘        └─┬─┬──────┘        └─────────┘
	//           ▲           │ │     ▲
	//           └───────────┘ └─────┘
	//              Detach     PushPull
	t.Run("admin document state transition test", func(t *testing.T) {
		ctx := context.Background()
		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		// 01. abnormal behavior on detached state
		d1 := document.New(helper.TestDocKey(t))
		assert.ErrorIs(t, cli.Detach(ctx, d1), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)

		// 02. abnormal behavior on attached state
		assert.NoError(t, cli.Attach(ctx, d1))
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)

		// 03. abnormal behavior on removed state
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.Error(t, err)

		assert.NoError(t, cli.Sync(ctx, client.WithDocKey(d1.Key())))
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Detach(ctx, d1), client.ErrDocumentNotAttached)
	})

	t.Run("admin removed document removal with watching test", func(t *testing.T) {
		ctx := context.Background()
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// 01. c1 attaches and watches d1.
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		wg := sync.WaitGroup{}
		wg.Add(1)
		rch, err := c1.Watch(watchCtx, d1)
		assert.NoError(t, err)
		go func() {
			defer wg.Done()

			for {
				resp := <-rch
				if resp.Err == io.EOF {
					assert.Fail(t, resp.Err.Error())
					return
				}
				assert.NoError(t, resp.Err)

				if resp.Type == client.DocumentsChanged {
					err := c1.Sync(ctx, client.WithDocKey(resp.Key))
					assert.NoError(t, err)
					return
				}
			}
		}()

		// 02. adminCli removes d1.
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String())
		assert.NoError(t, err)

		// 03. wait for watching document changed event.
		wg.Wait()
		assert.Equal(t, d1.Status(), document.StatusRemoved)
	})
}
