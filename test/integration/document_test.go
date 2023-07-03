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
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocument(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("attach/detach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}, "update k1 with v1")
		assert.NoError(t, err)

		err = c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		err = c1.Detach(ctx, doc, false)
		assert.NoError(t, err)
		assert.False(t, doc.IsAttached())

		doc2 := document.New(helper.TestDocKey(t))
		err = doc2.Update(func(root *json.Object) error {
			root.SetString("k1", "v2")
			return nil
		}, "update k1 with v2")

		err = c1.Attach(ctx, doc2)
		assert.NoError(t, err)
		assert.True(t, doc2.IsAttached())
		assert.Equal(t, `{"k1":"v2"}`, doc2.Marshal())

		doc3 := document.New(key.Key("invalid$key"))
		err = c1.Attach(ctx, doc3)
		assert.Error(t, err)
	})

	t.Run("detach removeIfNotAttached flag test", func(t *testing.T) {
		// 01. create a document and attach it to c1
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}, "update k1 with v1")
		assert.NoError(t, err)

		err = c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		// 02. detach with removeIfNotAttached option false
		err = c1.Detach(ctx, doc, false)
		assert.NoError(t, err)
		assert.False(t, doc.IsAttached())
		assert.Equal(t, doc.Status(), document.StatusDetached)

		// 03. attach again to c1 and check if it is attached normally
		err = c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		// 04. detach with removeIfNotAttached option true
		err = c1.Detach(ctx, doc, true)
		assert.NoError(t, err)
		assert.False(t, doc.IsAttached())
		assert.Equal(t, doc.Status(), document.StatusRemoved)
	})

	t.Run("concurrent complex test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewObject("k1").SetNewArray("k1.1").AddString("1", "2")
			return nil
		})
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewArray("k2").AddString("1", "2", "3")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			root.SetNewArray("k1").AddString("4", "5")
			root.SetNewArray("k2").AddString("6", "7")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object) error {
			root.Delete("k2")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("watch document changed event test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		_, err := c1.Watch(ctx, d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)

		err = c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		wg := sync.WaitGroup{}

		// 01. cli1 watches doc1.
		wg.Add(1)
		rch, err := c1.Watch(ctx, d1)
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

		// 02. cli2 updates doc2.
		err = d2.Update(func(root *json.Object) error {
			root.SetString("key", "value")
			return nil
		})
		assert.NoError(t, err)

		err = c2.Sync(ctx)
		assert.NoError(t, err)

		wg.Wait()

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("document tombstone test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetNewArray("k1").AddInteger(1, 2)
			return nil
		})
		assert.NoError(t, err)

		err = c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *json.Object) error {
			root.GetArray("k1").AddInteger(3)
			return nil
		})
		assert.NoError(t, err)

		prevArray := d1.Root().Get("k1")
		assert.Nil(t, prevArray.RemovedAt())

		err = d2.Update(func(root *json.Object) error {
			root.SetNewArray("k1")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.NotNil(t, prevArray.RemovedAt())
	})

	t.Run("single-client document deletion test", func(t *testing.T) {
		ctx := context.Background()

		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, cli.Close())
		}()
		d1 := document.New(helper.TestDocKey(t))

		// 01. client is not activated.
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrClientNotActivated)

		// 02. document is not attached.
		assert.NoError(t, cli.Activate(ctx))
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)

		// 03. document is attached.
		assert.NoError(t, cli.Attach(ctx, d1))
		assert.NoError(t, cli.Remove(ctx, d1))
		assert.Equal(t, document.StatusRemoved, d1.Status())

		// 04. try to update a removed document.
		assert.ErrorIs(t, d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		}), document.ErrDocumentRemoved)

		// 05. try to attach a removed document.
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)
	})

	t.Run("removed document creation test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and removes it.
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))
		assert.NoError(t, c1.Remove(ctx, d1))

		// 02. cli2 creates d2 with the same key.
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))

		// 03. cli1 creates d3 with the same key.
		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d3))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d3}, {c2, d2}})
	})

	t.Run("removed document pushpull test", func(t *testing.T) {
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
		assert.NoError(t, c1.Remove(ctx, d1))

		// 03. cli2 syncs and checks that d2 is removed.
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("removed document detachment test", func(t *testing.T) {
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

		// 02. cli1 removes d1 and cli2 detaches d2.
		assert.NoError(t, c1.Remove(ctx, d1))
		assert.NoError(t, c2.Detach(ctx, d2, false))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
	})

	t.Run("removed document removal test", func(t *testing.T) {
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
		assert.NoError(t, c1.Remove(ctx, d1))
		assert.NoError(t, c2.Remove(ctx, d2))
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
	t.Run("document state transition test", func(t *testing.T) {
		ctx := context.Background()
		cli, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		defer func() {
			assert.NoError(t, cli.Close())
		}()

		// 01. abnormal behavior on detached state
		d1 := document.New(helper.TestDocKey(t))
		assert.ErrorIs(t, cli.Detach(ctx, d1, false), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)

		// 02. abnormal behavior on attached state
		assert.NoError(t, cli.Attach(ctx, d1))
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)

		// 03. abnormal behavior on removed state
		assert.NoError(t, cli.Remove(ctx, d1))
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Detach(ctx, d1, false), client.ErrDocumentNotAttached)
	})

	t.Run("removed document removal with watching test", func(t *testing.T) {
		ctx := context.Background()
		watchCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// 01. c1 creates d1 without attaching.
		d1 := document.New(helper.TestDocKey(t))
		_, err := c1.Watch(watchCtx, d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)

		// 02. c1 attaches d1 and watches it.
		assert.NoError(t, c1.Attach(ctx, d1))
		_, err = c1.Watch(watchCtx, d1)
		assert.NoError(t, err)

		// 03. c1 removes d1 and watches it.
		assert.NoError(t, c1.Remove(ctx, d1))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		_, err = c1.Watch(watchCtx, d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)
	})
}

func TestDocumentWithProjects(t *testing.T) {
	ctx := context.Background()
	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { assert.NoError(t, adminCli.Close()) }()

	project1, err := adminCli.CreateProject(ctx, "project1")
	assert.NoError(t, err)

	project2, err := adminCli.CreateProject(ctx, "project2")
	assert.NoError(t, err)

	t.Run("watch document with different projects test", func(t *testing.T) {
		ctx := context.Background()

		// 01. c1 and c2 are in the same project and c3 is in another project.
		c1, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project1.PublicKey))
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c1.Close()) }()
		assert.NoError(t, c1.Activate(ctx))
		defer func() { assert.NoError(t, c1.Deactivate(ctx)) }()

		c2, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project1.PublicKey))
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c2.Close()) }()
		assert.NoError(t, c2.Activate(ctx))
		defer func() { assert.NoError(t, c2.Deactivate(ctx)) }()

		c3, err := client.Dial(defaultServer.RPCAddr(), client.WithAPIKey(project2.PublicKey))
		assert.NoError(t, err)
		defer func() { assert.NoError(t, c3.Close()) }()
		assert.NoError(t, c3.Activate(ctx))
		defer func() { assert.NoError(t, c3.Deactivate(ctx)) }()

		// 02. c1 and c2 watch the same document and c3 watches another document but the same key.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wg := sync.WaitGroup{}
		wg.Add(1)

		d1 := document.New(helper.TestDocKey(t))
		err = c1.Attach(ctx, d1)
		assert.NoError(t, err)
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		rch, err := c1.Watch(watch1Ctx, d1)
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
					responsePairs = append(responsePairs, watchResponsePair{
						Type: resp.Type,
					})
					return
				}

				if resp.Type == client.PeersChanged {
					peers := resp.PeersMapByDoc[d1.Key()]
					responsePairs = append(responsePairs, watchResponsePair{
						Type:  resp.Type,
						Peers: peers,
					})
				}
			}
		}()

		// c2 watches the same document, so c1 receives a peers changed event.
		expected = append(expected, watchResponsePair{
			Type: client.PeersChanged,
			Peers: map[string]types.Presence{
				c1.ID().String(): nil,
				c2.ID().String(): nil,
			},
		})
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		defer cancel2()
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// c2 updates the document, so c1 receives a documents changed event.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentsChanged,
		})
		assert.NoError(t, d2.Update(func(root *json.Object) error {
			root.SetString("key", "value")
			return nil
		}))

		// d3 is in another project, so c1 and c2 should not receive events.
		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, d3))
		watch3Ctx, cancel3 := context.WithCancel(ctx)
		defer cancel3()
		_, err = c3.Watch(watch3Ctx, d3)
		assert.NoError(t, err)
		assert.NoError(t, d3.Update(func(root *json.Object) error {
			root.SetString("key3", "value3")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))

		wg.Wait()

		assert.Equal(t, expected, responsePairs)
		assert.Equal(t, "{\"key\":\"value\"}", d1.Marshal())
		assert.Equal(t, "{\"key\":\"value\"}", d2.Marshal())
		assert.Equal(t, "{\"key3\":\"value3\"}", d3.Marshal())
	})
}
