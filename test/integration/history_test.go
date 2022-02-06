//go:build integration

/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

func TestHistory(t *testing.T) {
	clients := createActivatedClients(t, 2)
	c1 := clients[0]
	c2 := clients[1]
	defer cleanupClients(t, clients)

	t.Run("history test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.Collection, t.Name())
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.SetNewArray("todos")
			return nil
		}, "create todos"))
		assert.Equal(t, `{"todos":[]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("todos").AddString("buy coffee")
			return nil
		}, "buy coffee"))
		assert.Equal(t, `{"todos":["buy coffee"]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *proxy.ObjectProxy) error {
			root.GetArray("todos").AddString("buy bread")
			return nil
		}, "buy bread"))
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, d1.Marshal())

		assert.NoError(t, c1.Sync(ctx))

		changes, err := c2.FetchHistory(ctx, d1.Key())
		assert.NoError(t, err)
		assert.Len(t, changes, 3)

		assert.Equal(t, "create todos", changes[0].Message)
		assert.Equal(t, "buy coffee", changes[1].Message)
		assert.Equal(t, "buy bread", changes[2].Message)

		assert.Equal(t, `{"todos":[]}`, changes[0].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee"]}`, changes[1].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, changes[2].Snapshot)
	})
}
