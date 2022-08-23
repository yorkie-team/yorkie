//go:build integration && amd64

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
	"fmt"
	"log"
	"math"
	"reflect"
	"testing"

	"bou.ke/monkey"
	"github.com/yorkie-team/yorkie/pkg/document/json"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/server/backend/database/mongo"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestSnapshot(t *testing.T) {
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

	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer cleanupClients(t, clients)

	t.Run("snapshot test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(key.Key(t.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(key.Key(t.Name()))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// 01. Update changes over snapshot threshold.
		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			err := d1.Update(func(root *json.Object) error {
				root.SetInteger(fmt.Sprintf("%d", i), i)
				return nil
			})
			assert.NoError(t, err)
		}
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// 02. Makes local changes then pull a snapshot from the server.
		err = d2.Update(func(root *json.Object) error {
			root.SetString("key", "value")
			return nil
		})
		assert.NoError(t, err)

		err = c2.Sync(ctx)
		assert.NoError(t, err)
		assert.Equal(t, `"value"`, d2.RootObject().Get("key").Marshal())

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("text snapshot test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(key.Key(t.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

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
			{2, 2, "ㄱ"}, {2, 3, "구"},
			{2, 3, "굴"}, {2, 3, "구"},
			{3, 3, "ㄹ"}, {3, 4, "ㄹ"},
			{3, 4, "르"}, {3, 4, "름"},
		}

		for _, edit := range edits {
			err = d1.Update(func(root *json.Object) error {
				root.GetText("k1").Edit(edit.from, edit.to, edit.content)
				return nil
			})
			assert.NoError(t, err)
		}
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(key.Key(t.Name()))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		assert.Equal(t, `{"k1":"하늘구름"}`, d1.Marshal())
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("text snapshot with concurrent local change test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(key.Key(t.Name()))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object) error {
			root.SetNewText("k1")
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(key.Key(t.Name()))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			err = d1.Update(func(root *json.Object) error {
				root.GetText("k1").Edit(i, i, "x")
				return nil
			})
			assert.NoError(t, err)
		}

		err = d2.Update(func(root *json.Object) error {
			root.GetText("k1").Edit(0, 0, "o")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})

	t.Run("snapshot with purging chagnes test", func(t *testing.T) {
		serverConfig := helper.TestConfig()
		// Default SnapshotInterval is 0, SnashotThreshold must also be 0
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

		d1 := document.New(key.Key(t.Name()))
		assert.NoError(t, cli1.Attach(ctx, d1))
		defer func() { assert.NoError(t, cli1.Detach(ctx, d1)) }()

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
			0,
			math.MaxInt64,
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

		d2 := document.New(key.Key(t.Name()))
		assert.NoError(t, cli2.Attach(ctx, d2))
		defer func() { assert.NoError(t, cli2.Detach(ctx, d2)) }()

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
			0,
			math.MaxInt64,
		)
		assert.NoError(t, err)

		assert.Len(t, changes, 8)
	})
}
