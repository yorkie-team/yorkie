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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	monkey "github.com/undefinedlabs/go-mpatch"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server/backend/background"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestSnapshot(t *testing.T) {
	var b *background.Background
	patch, err := monkey.PatchInstanceMethodByName(
		reflect.TypeOf(b),
		"AttachGoroutine",
		func(
			_ *background.Background,
			f func(c context.Context),
			_ string,
		) {
			f(context.Background())
		},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer patch.Unpatch()

	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("snapshot test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		// 01. Update changes over snapshot threshold.
		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			err := d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetInteger(fmt.Sprintf("%d", i), i)
				return nil
			})
			assert.NoError(t, err)
		}
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		// 02. Makes local changes then pull a snapshot from the server.
		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
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

		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
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
			err = d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("k1").Edit(edit.from, edit.to, edit.content)
				return nil
			})
			assert.NoError(t, err)
		}
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		assert.Equal(t, `{"k1":[{"val":"하"},{"val":"늘"},{"val":"구"},{"val":"름"}]}`, d1.Marshal())
		assert.Equal(t, d1.Marshal(), d2.Marshal())
	})

	t.Run("text snapshot with concurrent local change test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestDocKey(t))
		err := c1.Attach(ctx, d1)
		assert.NoError(t, err)

		err = d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k1")
			return nil
		})
		assert.NoError(t, err)
		err = c1.Sync(ctx)
		assert.NoError(t, err)

		d2 := document.New(helper.TestDocKey(t))
		err = c2.Attach(ctx, d2)
		assert.NoError(t, err)

		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			err = d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetText("k1").Edit(i, i, "x")
				return nil
			})
			assert.NoError(t, err)
		}

		err = d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k1").Edit(0, 0, "o")
			return nil
		})
		assert.NoError(t, err)

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
	})
}
