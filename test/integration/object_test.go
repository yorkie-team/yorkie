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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestObject(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("causal object.set/delete test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

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
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k1")
			root.GetObject("k2").Delete("k2.2")
			return nil
		}, "nested update by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object set/delete simple test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k1")
			return nil
		}, "set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":{}}`, d1.Marshal())
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k1")
			root.SetString("k1", "v1")
			return nil
		}, "delete and set v1 by c1")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v1"}`, d1.Marshal())

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.Delete("k1")
			root.SetString("k1", "v2")
			return nil
		}, "delete and set v2 by c2")
		assert.NoError(t, err)
		assert.Equal(t, `{"k1":"v2"}`, d2.Marshal())
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent object.set test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// 01. concurrent set on same key
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v1")
			return nil
		}, "set k1 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k1", "v2")
			return nil
		}, "set k1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 02. concurrent set between ancestor descendant
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewObject("k2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k2", "v2")
			return nil
		}, "set k2 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetObject("k2").SetNewObject("k2.1").SetString("k2.1.1", "v2")
			return nil
		}, "set k2.1.1 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// 03. concurrent set between independent keys
		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k3", "v3")
			return nil
		}, "set k3 by c1")
		assert.NoError(t, err)
		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.SetString("k4", "v4")
			return nil
		}, "set k4 by c2")
		assert.NoError(t, err)
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

}
