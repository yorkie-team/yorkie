//go:build integration

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
	gojson "encoding/json"
	"fmt"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPresence(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("update presence by calling Document.Update test", func(t *testing.T) {
		// 01. Create a document and attach it to the clients
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		// 02. Update the root of the document and presence
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetString("key", "value")
			p.Set("updated", "true")
			return nil
		}))
		encoded, err := gojson.Marshal(d1.AllPresences())
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf(`{"%s":{"updated":"true"}}`, c1.ID()), string(encoded))

		// 03 Sync documents and check that the presence is updated on the other client
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		encoded, err = gojson.Marshal(d2.AllPresences())
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf(`{"%s":{"updated":"true"},"%s":{}}`, c1.ID(), c2.ID()), string(encoded))
	})

	t.Run("presence with snapshot test", func(t *testing.T) {
		// 01. Create a document and attach it to the clients
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		// 02. Update the root of the document and presence
		for i := 0; i < int(helper.SnapshotThreshold); i++ {
			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("key", "value")
				p.Set("updated", strconv.Itoa(i))
				return nil
			}))
		}
		encoded, err := gojson.Marshal(d1.AllPresences())
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf(`{"%s":{"updated":"9"}}`, c1.ID()), string(encoded))

		// 03 Sync documents and check that the presence is updated on the other client
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		encoded, err = gojson.Marshal(d2.AllPresences())
		assert.NoError(t, err)
		assert.Equal(t, fmt.Sprintf(`{"%s":{"updated":"9"},"%s":{}}`, c1.ID(), c2.ID()), string(encoded))
	})

	t.Run("presence with attach and detach test", func(t *testing.T) {
		// 01. Create a document and attach it to the clients
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithPresence(innerpresence.Presence{"key": c1.Key()})))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithPresence(innerpresence.Presence{"key": c2.Key()})))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		// 02. Check that the presence is updated on the other client.
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, innerpresence.Presence{"key": c1.Key()}, d1.MyPresence())
		assert.Equal(t, innerpresence.Presence{"key": c2.Key()}, d1.PresenceForTest(c2.ID().String()))
		assert.Equal(t, innerpresence.Presence{"key": c2.Key()}, d2.MyPresence())
		assert.Equal(t, innerpresence.Presence{"key": c1.Key()}, d2.PresenceForTest(c1.ID().String()))

		// 03. The first client detaches the document and check that the presence is updated on the other client.
		assert.NoError(t, c1.Detach(ctx, d1))
		assert.NoError(t, c2.Sync(ctx))
		assert.Equal(t, innerpresence.Presence{"key": c2.Key()}, d2.MyPresence())
		assert.Nil(t, d2.PresenceForTest(c1.ID().String()))
	})

	t.Run("presence-related events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type:      wr.Type,
							Presences: wr.Presences,
						})
					}
					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 03. Watch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)

		// 04. Update the second client's presence.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("updated", "true")
			return nil
		})
		assert.NoError(t, err)
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))

		// 05. Unwatch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentUnwatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		cancel2()

		wgEvents.Wait()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("unwatch after detach events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2))

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(10 * time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type:      wr.Type,
							Presences: wr.Presences,
						})
					}

					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 03. Watch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		_, cancel2, err := c2.Subscribe(d2)
		defer cancel2()
		assert.NoError(t, err)

		// 04. Update the second client's presence.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("updated", "true")
			return nil
		})
		assert.NoError(t, err)
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))

		// 05. Unwatch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentUnwatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		assert.NoError(t, c2.Detach(ctx, d2))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))
		wgEvents.Wait()

		cancel2()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("detach after unwatch events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2))

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(10 * time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}
					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type:      wr.Type,
							Presences: wr.Presences,
						})
					}

					if len(responsePairs) == 3 {
						return
					}
				}
			}
		}()

		// 03. Watch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		_, cancel2, err := c2.Subscribe(d2)
		defer cancel2()
		assert.NoError(t, err)

		// 04. Update the second client's presence.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("updated", "true")
			return nil
		})
		assert.NoError(t, err)
		expected = append(expected, watchResponsePair{
			Type: client.PresenceChanged,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		assert.NoError(t, c2.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))

		// 05. Unwatch the second client's document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentUnwatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		cancel2()

		assert.NoError(t, c2.Detach(ctx, d2))
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))

		wgEvents.Wait()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("watching multiple documents test", func(t *testing.T) {
		ctx := context.Background()

		// 01. Two clients attach two documents with the same key, and watch them.
		// But the second client also attaches another document.
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		d3 := document.New(helper.TestDocKey(t) + "2")
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
			defer func() {
				wgEvents.Done()
			}()
			for {
				select {
				case <-time.After(time.Second):
					assert.Fail(t, "timeout")
					return
				case wr := <-wrch:
					if wr.Err != nil {
						assert.Fail(t, "unexpected stream closing", wr.Err)
						return
					}

					if wr.Type != client.DocumentChanged {
						responsePairs = append(responsePairs, watchResponsePair{
							Type:      wr.Type,
							Presences: wr.Presences,
						})
					}

					if len(responsePairs) == 2 {
						return
					}
				}
			}
		}()

		// 03. The second client attaches a document with the same key as the first client's document
		// and another document with a different key.
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()
		assert.NoError(t, c2.Attach(ctx, d3))
		defer func() { assert.NoError(t, c2.Detach(ctx, d3)) }()

		// 04. The second client watches the documents attached by itself.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): d2.MyPresence(),
			},
		})
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		assert.NoError(t, c1.Sync(ctx, client.WithDocKey(helper.TestDocKey(t))))

		_, cancel3, err := c2.Subscribe(d3)
		assert.NoError(t, err)

		// 05. The second client unwatch the documents attached by itself.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentUnwatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		cancel2()
		cancel3()

		wgEvents.Wait()

		assert.Equal(t, expected, responsePairs)
	})
}
