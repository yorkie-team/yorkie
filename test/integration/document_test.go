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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"io"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

func TestDocument(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer cleanupClients(t, clients)

	t.Run("attach/detach test", func(t *testing.T) {
		ctx := context.Background()
		doc := document.New(key.Key(t.Name()))
		err := doc.Update(func(root *json.Object) error {
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

		doc2 := document.New(key.Key(t.Name()))
		err = doc2.Update(func(root *json.Object) error {
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

		d1 := document.New(key.Key(t.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(key.Key(t.Name()))
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

		d1 := document.New(key.Key(t.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(key.Key(t.Name()))
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
}
