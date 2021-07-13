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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestCounter(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("causal counter.increase test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewCounter("age", 1).
				Increase(2).
				Increase(2.5).
				Increase(-100000000).
				Increase(9223372036854775000)

			return nil
		}, "nested update by c1")
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("concurrent counter increase test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewCounter("age", 0)
			root.SetNewCounter("width", 0)
			root.SetNewCounter("height", 0)
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.Collection, t.Name())
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetCounter("age").
				Increase(1).
				Increase(2).
				Increase(-2).
				Increase(-.25)
			return nil
		})
		assert.NoError(t, err)

		err = d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetCounter("width").Increase(math.MaxInt32 + 100).Increase(10)
			return nil
		})
		assert.NoError(t, err)

		err = d2.Update(func(root *proxy.ObjectProxy) error {
			root.GetCounter("age").Increase(20)
			root.GetCounter("width").Increase(100).Increase(200)
			root.GetCounter("height").Increase(50)
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
