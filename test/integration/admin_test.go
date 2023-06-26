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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAdmin(t *testing.T) {
	ctx := context.Background()

	adminCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
	assert.NoError(t, err)
	_, err = adminCli.LogIn(ctx, "admin", "admin")
	assert.NoError(t, err)
	defer func() {
		assert.NoError(t, adminCli.Close())
	}()

	clients := activeClients(t, 1)
	c1 := clients[0]
	defer deactivateAndCloseClients(t, clients)

	t.Run("admin and client document deletion sync test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		d1 := document.New(helper.TestDocKey(t))

		// 01. admin tries to remove document that does not exist.
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())

		// 02. client creates a document then admin removes the document.
		assert.NoError(t, cli.Attach(ctx, d1))
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.NoError(t, err)
		assert.Equal(t, document.StatusAttached, d1.Status())

		// 03. client updates the document and sync to the server.
		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, document.StatusRemoved, d1.Status())
	})

	t.Run("document event propagation on removal test", func(t *testing.T) {
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
		err = adminCli.RemoveDocument(ctx, "default", d1.Key().String(), true)
		assert.NoError(t, err)

		// 03. wait for watching document changed event.
		wg.Wait()
		assert.Equal(t, d1.Status(), document.StatusRemoved)
	})

	t.Run("document removal without force test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()
		doc := document.New(helper.TestDocKey(t))

		// 01. try to remove document that does not exist.
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.Equal(t, codes.NotFound, status.Convert(err).Code())

		// 02. try to remove document that is attached by the client.
		assert.NoError(t, cli.Attach(ctx, doc))
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.Equal(t, codes.FailedPrecondition, status.Convert(err).Code())
		assert.Equal(t, document.StatusAttached, doc.Status())

		// 03. remove document that is detached by the client.
		assert.NoError(t, cli.Detach(ctx, doc, false))
		err = adminCli.RemoveDocument(ctx, "default", doc.Key().String(), false)
		assert.NoError(t, err)
		assert.Equal(t, document.StatusDetached, doc.Status())
	})
}
