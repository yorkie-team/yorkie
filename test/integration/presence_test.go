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

	"github.com/yorkie-team/yorkie/admin"
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
		for i := range int(helper.SnapshotThreshold) {
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
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		assert.NoError(t, c1.Sync(ctx))
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
						wgEvents.Done()
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
		wgEvents.Wait()
		wgEvents.Add(1)

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
		wgEvents.Wait()
		wgEvents.Add(1)

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

	t.Run("watch after attach events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
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
						wgEvents.Done()
					}
					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()

		// 3. After c1 syncing the change of c2's attachment, c2 watches the document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		assert.NoError(t, c1.Sync(ctx))
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		defer cancel2()

		wgEvents.Wait()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("attach after watch events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
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
						wgEvents.Done()
					}
					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()

		// 3. After c2 watches the document, c1 syncs the change of c2's attachment.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {},
			},
		})
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		defer cancel2()
		time.Sleep(100 * time.Millisecond) // wait until the window time for batch publishing
		assert.NoError(t, c1.Sync(ctx))

		wgEvents.Wait()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("unwatch after detach events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		assert.NoError(t, c1.Sync(ctx))

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
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
						wgEvents.Done()
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
		wgEvents.Wait()
		wgEvents.Add(1)

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
		wgEvents.Wait()
		wgEvents.Add(1)

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
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		assert.NoError(t, c1.Sync(ctx))

		// 02. Watch the first client's document.
		var expected []watchResponsePair
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
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
						wgEvents.Done()
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
		wgEvents.Wait()
		wgEvents.Add(1)

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
		wgEvents.Wait()
		wgEvents.Add(1)

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

	t.Run("watch after update events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
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
						wgEvents.Done()
					}
					if len(responsePairs) == 1 {
						return
					}
				}
			}
		}()

		// 3. After c1 syncing the change of c2's update, c2 watches the document.
		expected = append(expected, watchResponsePair{
			Type: client.DocumentWatched,
			Presences: map[string]innerpresence.Presence{
				c2.ID().String(): {"updated": "true"},
			},
		})
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("updated", "true")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)
		defer cancel2()

		wgEvents.Wait()
		assert.Equal(t, expected, responsePairs)
	})

	t.Run("watch after detach events test", func(t *testing.T) {
		// 01. Create two clients and documents and attach them.
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))

		// 02. Watch the first client's document.
		var responsePairs []watchResponsePair
		wgEvents := sync.WaitGroup{}
		wgEvents.Add(1)

		wrch, cancel1, err := c1.Subscribe(d1)
		defer cancel1()
		assert.NoError(t, err)
		go func() {
			for {
				select {
				case <-time.After(time.Second):
					wgEvents.Done()
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
						println("what???", wr.Type)
						assert.Fail(t, "unexpected presence event")
					}
				}
			}
		}()

		// 3. After c1 syncing the change of c2's detachment, c2 watches the document.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("updated", "true")
			return nil
		})
		assert.NoError(t, err)
		assert.NoError(t, c1.Sync(ctx))

		_, cancel2, err := c2.Subscribe(d2) // No events yet - before the window time for batch publishing
		assert.NoError(t, err)
		defer cancel2()
		assert.NoError(t, c2.Detach(ctx, d2))
		assert.NoError(t, c1.Sync(ctx))

		wgEvents.Wait()
		assert.Equal(t, 0, len(responsePairs))
	})

	t.Run("watching multiple documents test", func(t *testing.T) {
		ctx := context.Background()

		// 01. Two clients attach two documents with the same key, and watch them.
		// But the second client also attaches another document.
		d1 := document.New(helper.TestDocKey(t))
		d2 := document.New(helper.TestDocKey(t))
		d3 := document.New(helper.TestDocKey(t) + "2")
		assert.NoError(t, c1.Attach(ctx, d1, client.WithRealtimeSync()))
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
		assert.NoError(t, c2.Attach(ctx, d2, client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()
		assert.NoError(t, c2.Attach(ctx, d3, client.WithRealtimeSync()))
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

	t.Run("GetDocument presence filtering test", func(t *testing.T) {
		// 01. Get admin client and default project
		ctx := context.Background()

		adminCli, err := admin.Dial(defaultServer.RPCAddr(), admin.WithInsecure(true))
		assert.NoError(t, err)
		_, err = adminCli.LogIn(ctx, "admin", "admin")
		assert.NoError(t, err)
		defer func() {
			adminCli.Close()
		}()

		project, err := adminCli.GetProject(ctx, "default")
		assert.NoError(t, err)

		// 02. Create two document instances with the same key and attach clients
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithPresence(innerpresence.Presence{"key": c1.Key()}), client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()
		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithPresence(innerpresence.Presence{"key": c2.Key()}), client.WithRealtimeSync()))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		// 03. Subscribe both clients to the document
		_, cancel1, err := c1.Subscribe(d1)
		assert.NoError(t, err)
		defer cancel1()
		_, cancel2, err := c2.Subscribe(d2)
		assert.NoError(t, err)

		// Wait for subscription events to be processed on the server
		time.Sleep(100 * time.Millisecond)

		// 04. Sync clients to ensure presence information is updated
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))

		// 05. Call GetDocuments API and verify both presences are included
		resBody := post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_keys": ["%s"], "include_presences": true}`, project.Name, helper.TestDocKey(t)),
		)

		summaries := &documentSummaries{}
		gojson.Unmarshal(resBody, summaries)
		assert.Equal(t, 1, len(summaries.Documents))

		presences := summaries.Documents[0].Presences
		assert.Equal(t, 2, len(presences), "should include presences of both subscribed clients")
		assert.Contains(t, presences, c1.ID().String())
		assert.Contains(t, presences, c2.ID().String())

		// 06. Unsubscribe c2 from the document
		cancel2()

		time.Sleep(100 * time.Millisecond)

		// 07. Call GetDocuments API again and verify c2's presence is filtered out
		resBody = post(
			t,
			project,
			fmt.Sprintf("http://%s/yorkie.v1.AdminService/GetDocuments", defaultServer.RPCAddr()),
			fmt.Sprintf(`{"project_name": "%s", "document_keys": ["%s"], "include_presences": true}`, project.Name, helper.TestDocKey(t)),
		)

		summaries = &documentSummaries{}
		gojson.Unmarshal(resBody, summaries)
		assert.Equal(t, 1, len(summaries.Documents))

		presences = summaries.Documents[0].Presences
		assert.Equal(t, 1, len(presences), "should only include presence of subscribed client")
		assert.Contains(t, presences, c1.ID().String())
		assert.NotContains(t, presences, c2.ID().String(), "c2's presence should be filtered out")
	})
}
