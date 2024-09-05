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
	gojson "encoding/json"
	"errors"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"io"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocument(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("attach/detach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))
		err := doc.Update(func(root *json.Object, p *presence.Presence) error {
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

		doc2 := document.New(helper.TestDocKey(t))
		err = doc2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v2")
			return nil
		}, "update k1 with v2")

		err = c1.Attach(ctx, doc2)
		assert.NoError(t, err)
		assert.True(t, doc2.IsAttached())
		assert.Equal(t, `{"k1":"v2"}`, doc2.Marshal())

		doc3 := document.New("invalid$key")
		err = c1.Attach(ctx, doc3)
		assert.Error(t, err)
	})

	t.Run("reattach test", func(t *testing.T) {
		ctx := context.Background()

		// 01. reattach a detached document
		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		}))
		assert.NoError(t, c1.Detach(ctx, doc))
		assert.Equal(t, connect.CodeFailedPrecondition, connect.CodeOf(c1.Attach(ctx, doc)))

		// 02. attach a new document with the same key
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc2))
		assert.NoError(t, c1.Detach(ctx, doc2))

		// 03. reattach but without updating the document
		doc3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, doc3))
		assert.NoError(t, c1.Detach(ctx, doc3))
	})

	t.Run("detach removeIfNotAttached flag test", func(t *testing.T) {
		// 01. create a document and attach it to c1
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		// 02. detach with removeIfNotAttached option false
		err = c1.Detach(ctx, doc)
		assert.NoError(t, err)
		assert.False(t, doc.IsAttached())
		assert.Equal(t, doc.Status(), document.StatusDetached)

		// 03. attach again to c1 and check if it is attached normally
		doc = document.New(helper.TestDocKey(t))
		err = c1.Attach(ctx, doc)
		assert.NoError(t, err)
		assert.True(t, doc.IsAttached())

		// 04. detach with removeIfNotAttached option true
		err = c1.Detach(ctx, doc, client.WithRemoveIfNotAttached())
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

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k1").SetNewArray("k1.1").AddString("1", "2")
			return nil
		})
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k2").AddString("1", "2", "3")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("k1").AddString("4", "5")
			root.SetNewArray("k2").AddString("6", "7")
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k2")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("watch document changed event test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))

		_, _, err := c1.Subscribe(d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)

		err = c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		wg := sync.WaitGroup{}

		// 01. cli1 watches doc1.
		wg.Add(1)
		rch, _, err := c1.Subscribe(d1)
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

				if resp.Type == client.DocumentChanged {
					err := c1.Sync(ctx, client.WithDocKey(d1.Key()))
					assert.NoError(t, err)
					return
				}
			}
		}()

		// 02. cli2 updates doc2.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
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
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
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

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("k1").AddInteger(3)
			return nil
		})
		assert.NoError(t, err)

		prevArray := d1.Root().Get("k1")
		assert.Nil(t, prevArray.RemovedAt())

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
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
		assert.ErrorIs(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("k1", "v1")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Attach(ctx, d1))

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. cli1 updates d1 and removes it.
		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		assert.NoError(t, c2.Detach(ctx, d2))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		assert.Equal(t, d2.Status(), document.StatusRemoved)
	})

	t.Run("removed document removal test", func(t *testing.T) {
		ctx := context.Background()

		// 01. cli1 creates d1 and cli2 syncs.
		d1 := document.New(helper.TestDocKey(t))
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
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
		assert.ErrorIs(t, cli.Detach(ctx, d1), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)

		// 02. abnormal behavior on attached state
		assert.NoError(t, cli.Attach(ctx, d1))
		assert.ErrorIs(t, cli.Attach(ctx, d1), client.ErrDocumentNotDetached)

		// 03. abnormal behavior on removed state
		assert.NoError(t, cli.Remove(ctx, d1))
		assert.ErrorIs(t, cli.Remove(ctx, d1), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Sync(ctx, client.WithDocKey(d1.Key())), client.ErrDocumentNotAttached)
		assert.ErrorIs(t, cli.Detach(ctx, d1), client.ErrDocumentNotAttached)
	})

	t.Run("removed document removal with watching test", func(t *testing.T) {
		ctx := context.Background()

		// 01. c1 creates d1 without attaching.
		d1 := document.New(helper.TestDocKey(t))
		_, _, err := c1.Subscribe(d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)

		// 02. c1 attaches d1 and watches it.
		assert.NoError(t, c1.Attach(ctx, d1))
		_, _, err = c1.Subscribe(d1)
		assert.NoError(t, err)

		// 03. c1 removes d1 and watches it.
		assert.NoError(t, c1.Remove(ctx, d1))
		assert.Equal(t, d1.Status(), document.StatusRemoved)
		_, _, err = c1.Subscribe(d1)
		assert.ErrorIs(t, err, client.ErrDocumentNotAttached)
	})

	t.Run("broadcast to subscribers except publisher test", func(t *testing.T) {
		bch := make(chan string)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			// Send the unmarshaled payload to the channel to notify that this
			// subscriber receives the event.
			bch <- mentionedBy
			return nil
		}

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		rch1, _, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		rch2, _, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, d3))
		rch3, _, err := c3.Subscribe(d3)
		assert.NoError(t, err)
		d3.SubscribeBroadcastEvent("mention", handler)

		err = d3.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case <-rch3:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber,
					// except the publisher, successfully receives the event.
					assert.Equal(t, 2, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("no broadcasts to unsubscribers", func(t *testing.T) {
		bch := make(chan string)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			// Send the unmarshaled payload to the channel to notify that this
			// subscriber receives the event.
			bch <- mentionedBy
			return nil
		}

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		rch1, _, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		rch2, _, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		// d1 unsubscribes to the broadcast event.
		d1.UnsubscribeBroadcastEvent("mention")

		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if both the unsubscriber
					// and the publisher don't receive the event.
					assert.Equal(t, 0, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("unsubscriber can broadcast", func(t *testing.T) {
		bch := make(chan string)
		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			// Send the unmarshaled payload to the channel to notify that this
			// subscriber receives the event.
			bch <- mentionedBy
			return nil
		}

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		rch1, _, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		// c2 doesn't subscribe to the "mention" broadcast event.
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		rch2, _, err := c2.Subscribe(d2)
		assert.NoError(t, err)

		// The unsubscriber c2 broadcasts the "mention" event.
		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case <-rch1:
				case <-rch2:
				case m := <-bch:
					assert.Equal(t, "yorkie", m)
					rcv++
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber
					// receives the unsubscriber's event.
					assert.Equal(t, 1, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})

	t.Run("reject to broadcast unserializable payload", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		_, _, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", nil)

		// Try to broadcast an unserializable payload.
		ch := make(chan string)
		err = d1.Broadcast("mention", ch)
		assert.ErrorIs(t, document.ErrUnsupportedPayloadType, err)
	})

	t.Run("error occurs while handling broadcast event", func(t *testing.T) {
		var ErrBroadcastEventHandlingError = errors.New("")

		ctx := context.Background()
		handler := func(topic, publisher string, payload []byte) error {
			var mentionedBy string
			assert.Equal(t, topic, "mention")
			assert.NoError(t, gojson.Unmarshal(payload, &mentionedBy))
			return ErrBroadcastEventHandlingError
		}

		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		rch1, _, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		d1.SubscribeBroadcastEvent("mention", handler)

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		rch2, _, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		d2.SubscribeBroadcastEvent("mention", handler)

		err = d2.Broadcast("mention", "yorkie")
		assert.NoError(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			rcv := 0
			for {
				select {
				case resp := <-rch1:
					if resp.Err != nil {
						assert.Equal(t, resp.Type, client.DocumentBroadcast)
						assert.ErrorIs(t, resp.Err, ErrBroadcastEventHandlingError)
						rcv++
					}
				case <-rch2:
				case <-time.After(1 * time.Second):
					// Assuming that every subscriber can receive the broadcast
					// event within this timeout period, check if every subscriber
					// successfully receives the event.
					assert.Equal(t, 1, rcv)
					return
				case <-ctx.Done():
					return
				}
			}
		}()

		wg.Wait()
	})
}

func TestDocumentWithProjects(t *testing.T) {
	ctx := context.Background()
	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { adminCli.Close() }()

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
		rch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
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

				if resp.Type == client.DocumentChanged {
					err := c1.Sync(ctx, client.WithDocKey(d1.Key()))
					assert.NoError(t, err)
				} else {
					responsePairs = append(responsePairs, watchResponsePair{
						Type:      resp.Type,
						Presences: resp.Presences,
					})
				}
				if len(responsePairs) == 2 {
					return
				}
			}
		}()

		// c2 watches the same document, so c1 receives a document watched event.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)

		// c2 updates the document, so c1 receives a documents changed event.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("key", "value")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))

		// d3 is in another project, so c1 and c2 should not receive events.
		d3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, d3))
		_, cancel3, err := c3.Subscribe(d3)
		assert.NoError(t, err)
		assert.NoError(t, d3.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("key3", "value3")
			return nil
		}))
		assert.NoError(t, c3.Sync(ctx))

		// c2 unwatch the document, so c1 receives a document unwatched event.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentUnwatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		cancel3()
		cancel2()
		wg.Wait()

		assert.Equal(t, expected, responsePairs)
		assert.Equal(t, "{\"key\":\"value\"}", d1.Marshal())
		assert.Equal(t, "{\"key\":\"value\"}", d2.Marshal())
		assert.Equal(t, "{\"key3\":\"value3\"}", d3.Marshal())
	})

	clients := activeClients(t, 1)
	cli := clients[0]
	defer deactivateAndCloseClients(t, clients)

	t.Run("includeSnapshot test", func(t *testing.T) {
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, d1))
		defer func() { assert.NoError(t, cli.Detach(ctx, d1)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("testArray")
			return nil
		}, "add test array"))

		assert.NoError(t, cli.Sync(ctx))

		docs, err := adminCli.ListDocuments(ctx, "default", "", 0, true, false)
		assert.NoError(t, err)
		assert.Equal(t, "", docs[0].Snapshot)

		docs, err = adminCli.ListDocuments(ctx, "default", "", 0, true, true)
		assert.NoError(t, err)
		assert.NotEqual(t, 0, len(docs[0].Snapshot))
	})
}

func TestDocumentWithInitialRoot(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("attach with InitialRoot test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(helper.TestDocKey(t))

		// 01. attach and initialize document
		assert.NoError(t, c1.Attach(ctx, doc1, client.WithInitialRoot(map[string]any{
			"counter": json.NewCounter(0, crdt.LongCnt),
			"content": map[string]any{
				"x": 1,
				"y": 1,
			},
		})))
		assert.True(t, doc1.IsAttached())
		assert.Equal(t, `{"content":{"x":1,"y":1},"counter":0}`, doc1.Marshal())
		assert.NoError(t, c1.Sync(ctx))

		// 02. attach and initialize document with new fields and if key already exists, it will be discarded
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2, client.WithInitialRoot(map[string]any{
			"counter": json.NewCounter(1, crdt.LongCnt),
			"content": map[string]any{
				"x": 2,
				"y": 2,
			},
			"new": map[string]any{
				"k": "v",
			},
		})))
		assert.True(t, doc2.IsAttached())
		assert.Equal(t, `{"content":{"x":1,"y":1},"counter":0,"new":{"k":"v"}}`, doc2.Marshal())
	})

	t.Run("attach with InitialRoot after key deletion test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(helper.TestDocKey(t))

		// 01. client1 attach with initialRoot
		assert.NoError(t, c1.Attach(ctx, doc1, client.WithInitialRoot(map[string]any{
			"counter": json.NewCounter(1, crdt.LongCnt),
			"content": map[string]any{
				"x": 1,
				"y": 1,
			},
		})))
		assert.True(t, doc1.IsAttached())
		assert.Equal(t, `{"content":{"x":1,"y":1},"counter":1}`, doc1.Marshal())
		assert.NoError(t, c1.Sync(ctx))

		// 02. client2 attach with initialRoot and delete elements
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2))
		assert.True(t, doc2.IsAttached())
		assert.NoError(t, doc2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("content")
			root.Delete("counter")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))

		// 03. client3 attach with initialRoot and delete elements
		doc3 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, doc3, client.WithInitialRoot(map[string]any{
			"counter": json.NewCounter(3, crdt.LongCnt),
			"content": map[string]any{
				"x": 3,
				"y": 3,
			},
		})))
		assert.True(t, doc3.IsAttached())
		assert.Equal(t, `{"content":{"x":3,"y":3},"counter":3}`, doc3.Marshal())
	})

	t.Run("concurrent attach with InitialRoot test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(helper.TestDocKey(t))

		// 01. user1 attach with initialRoot and client doesn't sync
		assert.NoError(t, c1.Attach(ctx, doc1, client.WithInitialRoot(map[string]any{
			"first_writer": "user1",
		})))
		assert.True(t, doc1.IsAttached())
		assert.Equal(t, `{"first_writer":"user1"}`, doc1.Marshal())

		// 02. user2 attach with initialRoot and client doesn't sync
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2, client.WithInitialRoot(map[string]any{
			"first_writer": "user2",
		})))
		assert.True(t, doc2.IsAttached())
		assert.Equal(t, `{"first_writer":"user2"}`, doc2.Marshal())

		// 03. user1 sync first and user2 seconds
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// 04. user1's local document's first_writer was user1
		assert.Equal(t, `{"first_writer":"user1"}`, doc1.Marshal())
		assert.Equal(t, `{"first_writer":"user2"}`, doc2.Marshal())

		// 05. user1's local document's first_writer is overwritten by user2
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, `{"first_writer":"user2"}`, doc1.Marshal())
	})

	t.Run("attach with InitialRoot by same key test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(helper.TestDocKey(t))

		k1 := "key"
		k2 := "key"
		k3 := "key"
		k4 := "key"
		k5 := "key"
		assert.NoError(t, c1.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
			k1: 1,
			k2: 2,
			k3: 3,
			k4: 4,
			k5: 5,
		})))
		assert.True(t, doc.IsAttached())
		// The last value is used when the same key is used.
		assert.Equal(t, `{"key":5}`, doc.Marshal())
	})

	t.Run("attach with InitialRoot conflict type test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(helper.TestDocKey(t))

		// 01. attach with initialRoot and set counter
		assert.NoError(t, c1.Attach(ctx, doc1, client.WithInitialRoot(map[string]any{
			"k": json.NewCounter(1, crdt.LongCnt),
		})))
		assert.True(t, doc1.IsAttached())
		assert.NoError(t, c1.Sync(ctx))

		// 02. attach with initialRoot and set text
		doc2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, doc2, client.WithInitialRoot(map[string]any{
			"k": json.NewText(),
		})))
		assert.True(t, doc2.IsAttached())
		assert.NoError(t, c2.Sync(ctx))

		// 03. client2 try to update counter
		assert.Panics(t, func() { doc2.Root().GetText("k").Edit(0, 1, "a") })
	})

	t.Run("attach with initialRoot support type test", func(t *testing.T) {
		type (
			Myint    int
			MyStruct struct {
				M Myint
			}
			t1 struct {
				M string
			}
			T1 struct {
				M string
			}
			T2 struct {
				T1
				t1
				M string
			}
		)
		ctx := context.Background()
		nowTime := time.Now()
		tests := []struct {
			caseName     string
			input        any
			expectedJSON string
			expectPanic  bool
		}{
			// supported primitive types
			{"nil", nil, `{"k":null}`, false},
			{"int", 1, `{"k":1}`, false},
			{"int32", int32(1), `{"k":1}`, false},
			{"int64", int64(1), `{"k":1}`, false},
			{"float32", float32(1.1), `{"k":1.100000}`, false},
			{"float64", 1.1, `{"k":1.100000}`, false},
			{"string", "hello", `{"k":"hello"}`, false},
			{"bool", true, `{"k":true}`, false},
			{"time", nowTime, `{"k":"` + nowTime.Format(time.RFC3339) + `"}`, false},
			{"Myint", Myint(1), `{"k":1}`, false},

			// unsupported primitive types
			{"int8", int8(1), `{}`, true},
			{"int16", int16(1), `{}`, true},
			{"uint32", uint32(1), `{}`, true},
			{"uint64", uint64(1), `{}`, true},

			// supported slice, array types
			{"int slice", []int{1, 2, 3}, `{"k":[1,2,3]}`, false},
			{"&int slice", &[]int{1, 2, 3}, `{"k":[1,2,3]}`, false},
			{"any slice", []any{nil, 1, 1.0, "hello", true, nowTime, []int{1, 2, 3}}, `{"k":[null,1,1.000000,"hello",true,"` + nowTime.Format(time.RFC3339) + `",[1,2,3]]}`, false},
			{"&any slice", &[]any{nil, 1, 1.0, "hello", true, nowTime, []int{1, 2, 3}}, `{"k":[null,1,1.000000,"hello",true,"` + nowTime.Format(time.RFC3339) + `",[1,2,3]]}`, false},
			{"int array", [3]int{1, 2, 3}, `{"k":[1,2,3]}`, false},
			{"&int array", &[3]int{1, 2, 3}, `{"k":[1,2,3]}`, false},
			{"string array", [3]string{"a", "b", "c"}, `{"k":["a","b","c"]}`, false},
			{"&string array", &[3]string{"a", "b", "c"}, `{"k":["a","b","c"]}`, false},
			{"any array", [7]any{nil, 1, 1.0, "hello", true, nowTime, []int{1, 2, 3}}, `{"k":[null,1,1.000000,"hello",true,"` + nowTime.Format(time.RFC3339) + `",[1,2,3]]}`, false},
			{"&any array", &[7]any{nil, 1, 1.0, "hello", true, nowTime, []int{1, 2, 3}}, `{"k":[null,1,1.000000,"hello",true,"` + nowTime.Format(time.RFC3339) + `",[1,2,3]]}`, false},

			// supported map types
			{"string:any map", map[string]any{"a": nil, "b": 1, "c": 1.0, "d": "hello", "e": true, "f": nowTime, "g": []int{1, 2, 3}}, `{"k":{"a":null,"b":1,"c":1.000000,"d":"hello","e":true,"f":"` + nowTime.Format(time.RFC3339) + `","g":[1,2,3]}}`, false},
			{"&string:any map", &map[string]any{"a": nil, "b": 1, "c": 1.0, "d": "hello", "e": true, "f": nowTime, "g": []int{1, 2, 3}}, `{"k":{"a":null,"b":1,"c":1.000000,"d":"hello","e":true,"f":"` + nowTime.Format(time.RFC3339) + `","g":[1,2,3]}}`, false},

			// unsupported map types
			{"int map", map[int]int{1: 1, 2: 2}, `{}`, true},
			{"string map", map[string]string{"a": "a", "b": "b"}, `{}`, true},
			{"int map", map[int]any{1: 1, 2: 2}, `{}`, true},

			// supported JSON types
			{"json.Text", json.NewText(), `{"k":[]}`, false},
			{"*json.Text", *json.NewText(), `{"k":[]}`, false},
			{"json.Tree", json.NewTree(&json.TreeNode{ // 1: tree
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			}), `{"k":{"type":"doc","children":[{"type":"p","children":[{"type":"text","value":"ab"}]}]}}`, false},
			{"*json.Tree", *json.NewTree(&json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			}), `{"k":{"type":"doc","children":[{"type":"p","children":[{"type":"text","value":"ab"}]}]}}`, false},
			{"json.Counter", json.NewCounter(1, crdt.LongCnt), `{"k":1}`, false},
			{"*json.Counter", *json.NewCounter(1, crdt.LongCnt), `{"k":1}`, false},

			// struct types
			{"struct", MyStruct{M: 1}, `{"k":{"M":1}}`, false},
			{"struct pointer", &MyStruct{M: 1}, `{"k":{"M":1}}`, false},
			{"struct slice", []MyStruct{{M: 1}, {M: 2}}, `{"k":[{"M":1},{"M":2}]}`, false},
			{"struct array", [2]MyStruct{{M: 1}, {M: 2}}, `{"k":[{"M":1},{"M":2}]}`, false},
			{"anonymous struct", struct{ M string }{M: "hello"}, `{"k":{"M":"hello"}}`, false},
			{"anonymous struct pointer", &struct{ M string }{M: "hello"}, `{"k":{"M":"hello"}}`, false},
			{"anonymous struct slice", []struct{ M string }{{M: "a"}, {M: "b"}}, `{"k":[{"M":"a"},{"M":"b"}]}`, false},
			{"anonymous struct array", [2]struct{ M string }{{M: "a"}, {M: "b"}}, `{"k":[{"M":"a"},{"M":"b"}]}`, false},
			{"struct with embedded struct", T2{T1: T1{M: "a"}, t1: t1{M: "b"}, M: "c"}, `{"k":{"M":"c","T1":{"M":"a"}}}`, false},
			{"strut with unexported field", struct {
				t int
				s string
			}{t: 1, s: "hello"}, `{"k":{}}`, false},
			{"strut with unexported field pointer", &struct {
				t int
				s string
			}{t: 1, s: "hello"}, `{"k":{}}`, false},
			{"struct with slice", struct{ M []int }{M: []int{1, 2, 3}}, `{"k":{"M":[1,2,3]}}`, false},
			{"struct with slice pointer", &struct{ M []int }{M: []int{1, 2, 3}}, `{"k":{"M":[1,2,3]}}`, false},
			{"struct with array", struct{ M [3]int }{M: [3]int{1, 2, 3}}, `{"k":{"M":[1,2,3]}}`, false},
			{"struct with array pointer", &struct{ M [3]int }{M: [3]int{1, 2, 3}}, `{"k":{"M":[1,2,3]}}`, false},
			{"struct with struct", struct{ M MyStruct }{M: MyStruct{M: 1}}, `{"k":{"M":{"M":1}}}`, false},
			{"struct with struct pointer", &struct{ M MyStruct }{M: MyStruct{M: 1}}, `{"k":{"M":{"M":1}}}`, false},
			{"struct with json types", struct {
				T    json.Text
				C    json.Counter
				Tree json.Tree
			}{T: *json.NewText(), C: *json.NewCounter(1, crdt.LongCnt), Tree: *json.NewTree(&json.TreeNode{
				Type: "doc",
				Children: []json.TreeNode{{
					Type: "p", Children: []json.TreeNode{{Type: "text", Value: "ab"}},
				}},
			})}, `{"k":{"C":1,"T":[],"Tree":{"type":"doc","children":[{"type":"p","children":[{"type":"text","value":"ab"}]}]}}}`, false},

			// unsupported struct types
			{"struct with unsupported map", struct{ M map[string]int }{M: map[string]int{"a": 1, "b": 2}}, `{}`, true},
			{"struct with unsupported primitive type", struct{ M int8 }{M: 1}, `{}`, true},

			{"func", func(a int, b int) int { return a + b }, `{}`, true},
		}
		for _, tt := range tests {
			t.Run(tt.caseName, func(t *testing.T) {
				doc := document.New(helper.TestDocKey(t))
				val := func() {
					assert.NoError(t, c1.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
						"k": tt.input,
					})))
				}
				if tt.expectPanic {
					assert.PanicsWithValue(t, "unsupported type", val)
				} else {
					assert.NotPanics(t, val)
				}
				assert.Equal(t, tt.expectedJSON, doc.Marshal())
			})
		}
	})
}
