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

package client_test

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/testhelper"
	"github.com/yorkie-team/yorkie/yorkie"
)

var testYorkie *yorkie.Yorkie

func TestMain(m *testing.M) {
	y := testhelper.TestYorkie()
	if err := y.Start(); err != nil {
		log.Fatal(err)
	}
	testYorkie = y
	code := m.Run()
	if testYorkie != nil {
		if err := testYorkie.Shutdown(true); err != nil {
			log.Println(err)
		}
	}
	os.Exit(code)
}

func TestClient(t *testing.T) {
	t.Run("new/close test", func(t *testing.T) {
		cli, err := client.NewClient(testYorkie.RPCAddr())
		assert.Nil(t, err)

		defer func() {
			err := cli.Close()
			assert.Nil(t, err)
		}()
	})

	t.Run("activate/deactivate test", func(t *testing.T) {
		cli, err := client.NewClient(testYorkie.RPCAddr())
		if err != nil {
			t.Fatal(err)
		}

		defer func() {
			err := cli.Close()
			assert.Nil(t, err)
		}()

		ctx := context.Background()

		err = cli.Activate(ctx)
		assert.Nil(t, err)
		assert.True(t, cli.IsActive())

		// Already activated
		err = cli.Activate(ctx)
		assert.Nil(t, err)
		assert.True(t, cli.IsActive())

		err = cli.Deactivate(ctx)
		assert.Nil(t, err)
		assert.False(t, cli.IsActive())

		// Already deactivated
		err = cli.Deactivate(ctx)
		assert.Nil(t, err)
		assert.False(t, cli.IsActive())
	})
}

func TestClientAndDocument(t *testing.T) {
	clients := getActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer func() {
		cleanupClients(t, clients)
	}()

	t.Run("attach/detach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(testhelper.Collection, t.Name())
		err := doc.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "k1")
			return nil
		}, "update k1 with k1")
		assert.Nil(t, err)

		err = c1.Attach(ctx, doc)
		assert.Nil(t, err)
		assert.True(t, doc.IsAttached())

		err = c1.Detach(ctx, doc)
		assert.Nil(t, err)
		assert.False(t, doc.IsAttached())
	})

	t.Run("causal nested array test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").
				AddString("v1").
				AddNewArray().AddString("1").AddString("2").AddString("3")
			return nil
		}, "nested update by c1")
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("causal primitive data test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").
				SetBool("k1.1", true).
				SetInteger("k1.2", 2147483647).
				SetLong("k1.3", 9223372036854775807).
				SetDouble("1.4", 1.79).
				SetString("k1.5", "4").
				SetBytes("k1.6", []byte{65, 66}).
				SetDate("k1.7", time.Now())

			root.SetNewArray("k2").
				AddBool(true).
				AddInteger(1).
				AddLong(2).
				AddDouble(3.0).
				AddString("4").
				AddBytes([]byte{65}).
				AddDate(time.Now())

			return nil
		}, "nested update by c1")
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("causal object.set/remove test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").
				SetString("k1.1", "v1").
				SetString("k1.2", "v2").
				SetString("k1.3", "v3")
			root.SetNewObject("k2").
				SetString("k2.1", "v4").
				SetString("k2.2", "v5").
				SetString("k2.3", "v6")
			return nil
		}, "nested update by c1")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.GetObject("k2").Remove("k2.2")
			return nil
		}, "nested update by c1")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object set/remove simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":{}}`, d1.Marshal())
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.SetString("k1", "v1")
			return nil
		}, "remove and set v1 by c1")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":"v1"}`, d1.Marshal())

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.SetString("k1", "v2")
			return nil
		}, "remove and set v2 by c2")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":"v2"}`, d2.Marshal())
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object.set test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		// 01. concurrent set on same key
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "set k1 by c1")
		assert.Nil(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v2")
			return nil
		}, "set k1 by c2")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. concurrent set between ancestor descendant
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k2")
			return nil
		}, "set k2 by c1")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k2", "v2")
			return nil
		}, "set k2 by c1")
		assert.Nil(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetObject("k2").SetNewObject("k2.1").SetString("k2.1.1", "v2")
			return nil
		}, "set k2.1.1 by c2")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. concurrent set between independent keys
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k3", "v3")
			return nil
		}, "set k3 by c1")
		assert.Nil(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k4", "v4")
			return nil
		}, "set k4 by c2")
		assert.Nil(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/remove simple test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1").AddString("v2")
			return nil
		}, "add v1, v2 by c1")
		assert.Nil(t, err)

		err = c1.Sync(ctx)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").Remove(1)
			return nil
		}, "remove v2 by c1")
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v3")
			return nil
		}, "add v3 by c2")
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent array add/remove test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1")
			return nil
		}, "new array and add v1")
		assert.Nil(t, err)
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v2").AddString("v3")
			root.GetArray("k1").Remove(1)
			return nil
		}, "add v2, v3 and remove v2 by c1")
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v4").AddString("v5")
			return nil
		}, "add v4, v5 by c2")
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent complex test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").SetNewArray("k1.1").AddString("1").AddString("2")
			return nil
		})
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k2").AddString("1").AddString("2").AddString("3")
			return nil
		})
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("4").AddString("5")
			root.SetNewArray("k2").AddString("6").AddString("7")
			return nil
		})
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k2")
			return nil
		})
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("text test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.Nil(t, err)
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(0, 0, "ABCD")
			return nil
		}, "edit 0,0 ABCD by c1")
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(0, 0, "1234")
			return nil
		}, "edit 0,0 1234 by c2")
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "XX")
			return nil
		}, "edit 2,3 XX by c1")
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "YY")
			return nil
		}, "edit 2,3 YY by c2")
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(4, 5, "ZZ")
			return nil
		}, "edit 4,5 ZZ by c1")
		assert.Nil(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "TT")
			return nil
		}, "edit 2,3 TT by c2")
		assert.Nil(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("watch test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)

		rch := c1.Watch(ctx, d1)
		go func() {
			defer wg.Done()

			resp := <-rch
			if resp.Err == io.EOF {
				return
			}
			assert.Nil(t, resp.Err)

			err := c1.Sync(ctx, resp.Keys...)
			assert.Nil(t, err)
		}()

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("key", "value")
			return nil
		})
		assert.Nil(t, err)

		err = c2.Sync(ctx)
		assert.Nil(t, err)

		wg.Wait()

		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("snapshot test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(testhelper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.Nil(t, err)

		d2 := document.New(testhelper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.Nil(t, err)

		// 01. Updates changes over snapshot threshold.
		for i := 0; i < testhelper.SnapshotThreshold + 1; i++ {
			err := d1.Update(func(root *proxy.ObjectProxy) error {
				root.SetInteger(fmt.Sprintf("%d", i), i)
				return nil
			})
			assert.Nil(t, err)
		}
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		// NOTE: waiting for snapshot.
		time.Sleep(500 * time.Millisecond)

		// 02. Makes local changes then pull a snapshot from the agent.
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("key", "value")
			return nil
		})
		assert.Nil(t, err)

		err = c2.Sync(ctx)
		assert.Nil(t, err)
		assert.Equal(t, `"value"`, d2.RootObject().Get("key").Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}

type clientAndDocPair struct {
	cli *client.Client
	doc *document.Document
}

func syncClientsThenAssertEqual(t *testing.T, pairs []clientAndDocPair) {
	assert.True(t, len(pairs) > 1)
	ctx := context.Background()
	// Save own changes and get previous changes.
	for i, pair := range pairs {
		fmt.Printf("before doc%d: %s\n", i+1, pair.doc.Marshal())
		err := pair.cli.Sync(ctx)
		assert.Nil(t, err)
	}

	// Get last client changes.
	// Last client get all precede changes in above loop.
	for _, pair := range pairs[:len(pairs)-1] {
		err := pair.cli.Sync(ctx)
		assert.Nil(t, err)
	}

	// Assert start.
	expected := pairs[0].doc.Marshal()
	fmt.Printf("after d1: %s\n", expected)
	for i, pair := range pairs[1:] {
		v := pair.doc.Marshal()
		fmt.Printf("after d%d: %s\n", i+2, v)
		assert.Equal(t, expected, v)
	}
}

func getActivatedClients(t *testing.T, n int) (clients []*client.Client) {
	for i := 0; i < n; i++ {
		c, err := client.NewClient(testYorkie.RPCAddr())
		assert.Nil(t, err)
		err = c.Activate(context.Background())
		assert.Nil(t, err)
		clients = append(clients, c)
	}
	return
}

func cleanupClients(t *testing.T, clients []*client.Client) {
	for _, c := range clients {
		err := c.Deactivate(context.Background())
		assert.Nil(t, err)
		err = c.Close()
		assert.Nil(t, err)
	}
}
