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

	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestRetention(t *testing.T) {
	var b *background.Background
	patch, err := monkey.PatchInstanceMethodByName(
		reflect.TypeOf(b),
		"AttachGoroutine",
		func(_ *background.Background, f func(c context.Context)) {
			f(context.Background())
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer patch.Unpatch()

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

		adminCli := helper.CreateAdminCli(t, testServer.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, adminCli.Close()) }()

		ctx := context.Background()

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))
		defer func() { assert.NoError(t, cli.Detach(ctx, doc, false)) }()

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

	t.Run("snapshot with purging changes test", func(t *testing.T) {
		serverConfig := helper.TestConfig()
		// Default SnapshotInterval is 0, SnapshotThreshold must also be 0
		serverConfig.Backend.SnapshotThreshold = 0
		serverConfig.Backend.SnapshotWithPurgingChanges = true
		testServer, err := server.New(serverConfig)
		if err != nil {
			log.Fatal(err)
		}

		if err := testServer.Start(); err != nil {
			logging.DefaultLogger().Fatal(err)
		}

		cli1, err := client.Dial(
			testServer.RPCAddr(),
			client.WithPresence(types.Presence{"name": fmt.Sprintf("name-%d", 0)}),
		)
		assert.NoError(t, err)

		err = cli1.Activate(context.Background())
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, cli1.Deactivate(context.Background()))
			assert.NoError(t, cli1.Close())
		}()

		ctx := context.Background()
		d1 := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli1.Attach(ctx, d1))
		defer func() { assert.NoError(t, cli1.Detach(ctx, d1, false)) }()

		err = d1.Update(func(root *json.Object) error {
			root.SetNewText("k1")
			return nil
		})
		assert.NoError(t, err)

		var edits = []struct {
			from    int
			to      int
			content string
		}{
			{0, 0, "ㅎ"}, {0, 1, "하"},
			{0, 1, "한"}, {0, 1, "하"},
			{1, 1, "느"}, {1, 2, "늘"},
		}

		// Create 6 changes
		for _, edit := range edits {
			err = d1.Update(func(root *json.Object) error {
				root.GetText("k1").Edit(edit.from, edit.to, edit.content)
				return nil
			})
			assert.NoError(t, err)
			err = cli1.Sync(ctx)
			assert.NoError(t, err)
		}

		mongoConfig := &mongo.Config{
			ConnectionTimeout: "5s",
			ConnectionURI:     "mongodb://localhost:27017",
			YorkieDatabase:    helper.TestDBName(),
			PingTimeout:       "5s",
		}
		assert.NoError(t, mongoConfig.Validate())

		mongoCli, err := mongo.Dial(mongoConfig)
		assert.NoError(t, err)

		docInfo, err := mongoCli.FindDocInfoByKey(
			ctx,
			"000000000000000000000000",
			d1.Key(),
		)
		assert.NoError(t, err)

		changes, err := mongoCli.FindChangesBetweenServerSeqs(
			ctx,
			docInfo.ID,
			change.InitialServerSeq,
			change.MaxServerSeq,
		)
		assert.NoError(t, err)

		// When the snapshot is created, changes before minSyncedServerSeq are deleted, so two changes remain.
		// Since minSyncedServerSeq is one older from the most recent ServerSeq,
		// one is most recent ServerSeq and one is one older from the most recent ServerSeq
		assert.Len(t, changes, 2)

		cli2, err := client.Dial(
			testServer.RPCAddr(),
			client.WithPresence(types.Presence{"name": fmt.Sprintf("name-%d", 1)}),
		)
		assert.NoError(t, err)

		err = cli2.Activate(context.Background())
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, cli2.Deactivate(context.Background()))
			assert.NoError(t, cli2.Close())
		}()

		d2 := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli2.Attach(ctx, d2))
		defer func() { assert.NoError(t, cli2.Detach(ctx, d2, false)) }()

		// Create 6 changes
		for _, edit := range edits {
			err = d2.Update(func(root *json.Object) error {
				root.GetText("k1").Edit(edit.from, edit.to, edit.content)
				return nil
			})
			assert.NoError(t, err)
			err = cli2.Sync(ctx)
			assert.NoError(t, err)
		}

		changes, err = mongoCli.FindChangesBetweenServerSeqs(
			ctx,
			docInfo.ID,
			change.InitialServerSeq,
			change.MaxServerSeq,
		)
		assert.NoError(t, err)

		assert.Len(t, changes, 8)
	})
}
