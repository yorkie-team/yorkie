//go:build integration && amd64

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
	"fmt"
	"log"
	"reflect"
	"testing"

	"bou.ke/monkey"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRetention(t *testing.T) {
	var b *background.Background
	monkey.PatchInstanceMethod(
		reflect.TypeOf(b),
		"AttachGoroutine",
		func(_ *background.Background, f func(c context.Context)) {
			f(context.Background())
		},
	)
	defer monkey.UnpatchInstanceMethod(
		reflect.TypeOf(b),
		"AttachGoroutine",
	)

	t.Run("history test with purging changes", func(t *testing.T) {
		conf := helper.TestConfig()
		conf.Backend.SnapshotWithPurgingChanges = true
		testServer, err := server.New(conf)
		if err != nil {
			log.Fatal(err)
		}

		if err := testServer.Start(); err != nil {
			logging.DefaultLogger().Fatal(err)
		}

		cli, err := client.Dial(
			testServer.RPCAddr(),
			client.WithPresence(types.Presence{"name": fmt.Sprintf("name-%d", 0)}),
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(context.Background()))
		defer func() {
			assert.NoError(t, cli.Deactivate(context.Background()))
			assert.NoError(t, cli.Close())
		}()

		adminCli := helper.CreateAdminCli(t, testServer.AdminAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, adminCli.Close()) }()

		ctx := context.Background()

		doc := document.New(key.Key(t.Name()))
		assert.NoError(t, cli.Attach(ctx, doc))
		defer func() { assert.NoError(t, cli.Detach(ctx, doc)) }()

		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.SetNewArray("todos")
			return nil
		}, "create todos"))
		assert.Equal(t, `{"todos":[]}`, doc.Marshal())

		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.GetArray("todos").AddString("buy coffee")
			return nil
		}, "buy coffee"))
		assert.Equal(t, `{"todos":["buy coffee"]}`, doc.Marshal())

		assert.NoError(t, doc.Update(func(root *json.Object) error {
			root.GetArray("todos").AddString("buy bread")
			return nil
		}, "buy bread"))
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, doc.Marshal())
		assert.NoError(t, cli.Sync(ctx))

		changes, err := adminCli.ListChangeSummaries(ctx, "default", doc.Key(), 0, 0, true)
		assert.NoError(t, err)
		assert.Len(t, changes, 3)

		assert.NoError(t, cli.Sync(ctx))

		changes2, err := adminCli.ListChangeSummaries(ctx, "default", doc.Key(), 0, 0, true)
		assert.NoError(t, err)
		assert.Len(t, changes2, 0)
	})
}
