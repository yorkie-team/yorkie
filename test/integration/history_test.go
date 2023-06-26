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
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestHistory(t *testing.T) {
	clients := activeClients(t, 1)
	cli := clients[0]
	defer deactivateAndCloseClients(t, clients)

	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { assert.NoError(t, adminCli.Close()) }()

	t.Run("history test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, d1))
		defer func() { assert.NoError(t, cli.Detach(ctx, d1, false)) }()

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.SetNewArray("todos")
			return nil
		}, "create todos"))
		assert.Equal(t, `{"todos":[]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetArray("todos").AddString("buy coffee")
			return nil
		}, "buy coffee"))
		assert.Equal(t, `{"todos":["buy coffee"]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *json.Object) error {
			root.GetArray("todos").AddString("buy bread")
			return nil
		}, "buy bread"))
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, d1.Marshal())
		assert.NoError(t, cli.Sync(ctx))

		changes, err := adminCli.ListChangeSummaries(ctx, "default", d1.Key(), 0, 0, true)
		assert.NoError(t, err)
		assert.Len(t, changes, 3)

		assert.Equal(t, "create todos", changes[2].Message)
		assert.Equal(t, "buy coffee", changes[1].Message)
		assert.Equal(t, "buy bread", changes[0].Message)

		assert.Equal(t, `{"todos":[]}`, changes[2].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee"]}`, changes[1].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, changes[0].Snapshot)
	})
}
