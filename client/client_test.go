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
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
)

const (
	testCollection = "test-col"
)

func syncThenAssertEqual(
	t *testing.T,
	c1 *client.Client,
	c2 *client.Client,
	doc1 *document.Document,
	doc2 *document.Document,
) {
	ctx := context.Background()
	fmt.Printf(
		"before doc1: %s\nbefore doc2: %s\n",
		doc1.Marshal(),
		doc2.Marshal(),
	)

	err := c1.Sync(ctx)
	assert.Nil(t, err)

	err = c2.Sync(ctx)
	assert.Nil(t, err)

	err = c1.Sync(ctx)
	assert.Nil(t, err)

	fmt.Printf(
		"after doc1: %s\nafter doc2: %s\n",
		doc1.Marshal(),
		doc2.Marshal(),
	)
	assert.Equal(t, doc1.Marshal(), doc2.Marshal())
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
		doc := document.New(testCollection, t.Name())
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
		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").
				AddString("v1").
				AddNewArray().AddString("1").AddString("2").AddString("3")
			return nil
		}, "nested update by c1")
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("causal primitive data test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
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

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("causal object.set/remove test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
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
		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.GetObject("k2").Remove("k2.2")
			return nil
		}, "nested update by c1")
		assert.Nil(t, err)
		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("concurrent object set/remove simple test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":{}}`, doc1.Marshal())
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.SetString("k1", "v1")
			return nil
		}, "remove and set v1 by c1")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":"v1"}`, doc1.Marshal())

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k1")
			root.SetString("k1", "v2")
			return nil
		}, "remove and set v2 by c2")
		assert.Nil(t, err)
		assert.Equal(t, `{"k1":"v2"}`, doc2.Marshal())
		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("concurrent object.set test", func(t *testing.T) {
		ctx := context.Background()
		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		// 01. concurrent set on same key
		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "set k1 by c1")
		assert.Nil(t, err)
		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v2")
			return nil
		}, "set k1 by c2")
		assert.Nil(t, err)
		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		// 02. concurrent set between ancestor descendant
		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k2")
			return nil
		}, "set k2 by c1")
		assert.Nil(t, err)
		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k2", "v2")
			return nil
		}, "set k2 by c1")
		assert.Nil(t, err)
		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetObject("k2").SetNewObject("k2.1").SetString("k2.1.1", "v2")
			return nil
		}, "set k2.1.1 by c2")
		assert.Nil(t, err)
		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		// 03. concurrent set between independent keys
		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k3", "v3")
			return nil
		}, "set k3 by c1")
		assert.Nil(t, err)
		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k4", "v4")
			return nil
		}, "set k4 by c2")
		assert.Nil(t, err)
		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("concurrent array add/remove simple test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1").AddString("v2")
			return nil
		}, "add v1, v2 by c1")
		assert.Nil(t, err)

		err = c1.Sync(ctx)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").Remove(1)
			return nil
		}, "remove v2 by c1")
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v3")
			return nil
		}, "add v3 by c2")
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("concurrent array add/remove test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("v1")
			return nil
		}, "new array and add v1")
		assert.Nil(t, err)
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v2").AddString("v3")
			root.GetArray("k1").Remove(1)
			return nil
		}, "add v2, v3 and remove v2 by c1")
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("k1").AddString("v4").AddString("v5")
			return nil
		}, "add v4, v5 by c2")
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("concurrent complex test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1").SetNewArray("k1.1").AddString("1").AddString("2")
			return nil
		})
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k2").AddString("1").AddString("2").AddString("3")
			return nil
		})
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("k1").AddString("4").AddString("5")
			root.SetNewArray("k2").AddString("6").AddString("7")
			return nil
		})
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.Remove("k2")
			return nil
		})
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("text test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewText("k1")
			return nil
		}, "set a new text by c1")
		assert.Nil(t, err)
		err = c1.Sync(ctx)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(0, 0, "ABCD")
			return nil
		}, "edit 0,0 ABCD by c1")
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(0, 0, "1234")
			return nil
		}, "edit 0,0 1234 by c2")
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "XX")
			return nil
		}, "edit 2,3 XX by c1")
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "YY")
			return nil
		}, "edit 2,3 YY by c2")
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)

		err = doc1.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(4, 5, "ZZ")
			return nil
		}, "edit 4,5 ZZ by c1")
		assert.Nil(t, err)

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.GetText("k1").Edit(2, 3, "TT")
			return nil
		}, "edit 2,3 TT by c2")
		assert.Nil(t, err)

		syncThenAssertEqual(t, c1, c2, doc1, doc2)
	})

	t.Run("watch test", func(t *testing.T) {
		ctx := context.Background()

		doc1 := document.New(testCollection, t.Name())
		err := c1.Attach(ctx, doc1)
		assert.Nil(t, err)

		doc2 := document.New(testCollection, t.Name())
		err = c2.Attach(ctx, doc2)
		assert.Nil(t, err)

		wg := sync.WaitGroup{}
		wg.Add(1)

		rch := c1.Watch(ctx, doc1)
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

		err = doc2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("key", "value")
			return nil
		})
		assert.Nil(t, err)

		err = c2.Sync(ctx)
		assert.Nil(t, err)

		wg.Wait()

		assert.Equal(t, doc1.Marshal(), doc2.Marshal())
	})
}
