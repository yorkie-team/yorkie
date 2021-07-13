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
	"time"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocument(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("attach/detach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.Collection, t.Name())
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "update k1 with v1")
		assert.NoError(t, err)

		err = c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		err = c1.Detach(ctx, doc)
		assert.NoError(t, err)
		assert.False(t, doc.IsAttached())

		doc2 := document.New(helper.Collection, t.Name())
		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v2")
			return nil
		}, "update k1 with v2")

		err = c1.Attach(ctx, doc2)
		assert.NoError(t, err)
		assert.True(t, doc2.IsAttached())
		assert.Equal(t, `{"k1":"v2"}`, doc2.Marshal())
	})

	t.Run("concurrent complex test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").SetNewArray("k1.1").AddString("1", "2")
			return nil
		})
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k2").AddString("1", "2", "3")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("4", "5")
			root.SetNewArray("k2").AddString("6", "7")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k2")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("watch document changed event test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
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
					err := c1.Sync(ctx, resp.Keys...)
					assert.NoError(t, err)
					return
				}
			}
		}()

		// 02. cli2 updates doc2.
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("key", "value")
			return nil
		})
		assert.NoError(t, err)

		err = c2.Sync(ctx)
		assert.NoError(t, err)

		wg.Wait()

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("WatchStarted and PeersChanged event test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		d2 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		// 01. WatchStarted is triggered when starting to watch a document
		watch1Ctx, cancel1 := context.WithCancel(ctx)
		defer cancel1()
		wrch, err := c1.Watch(watch1Ctx, d1)
		assert.NoError(t, err)
		go func() {
			defer wgEvents.Done()
			for {
				select {
				case <- time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err == io.EOF || status.Code(wr.Err) == codes.Canceled {
						assert.Fail(t, "unexpected stream closing")
						return
					}

					if wr.Type == client.PeersChanged {
						peers := wr.PeersMapByDoc[d1.Key().BSONKey()]
						responsePairs = append(responsePairs, watchResponsePair{Type: wr.Type, peers: peers})

						if len(peers) == 1 {
							return
						}
					}
				}
			}
		}()

		// 02. PeersChanged is triggered when another client watches the document
		watch2Ctx, cancel2 := context.WithCancel(ctx)
		_, err = c2.Watch(watch2Ctx, d2)
		assert.NoError(t, err)

		// 03. PeersChanged is triggered when another client closes the watch
		cancel2()

		wgEvents.Wait()

		assert.Equal(t, responsePairs, []watchResponsePair{{
			Type:  client.PeersChanged,
			peers: map[string]client.Metadata{
				c1.ID().String(): c1.Metadata(),
				c2.ID().String(): c2.Metadata(),
			},
		}, {
			Type:  client.PeersChanged,
			peers: map[string]client.Metadata{
				c1.ID().String(): c1.Metadata(),
			},
		}})
	})
}
