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
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestHistory(t *testing.T) {
	clients := activeClients(t, 1)
	cli := clients[0]
	defer deactivateAndCloseClients(t, clients)

	adminCli := helper.CreateAdminCli(t, defaultServer.RPCAddr())
	defer func() { adminCli.Close() }()

	t.Run("history test", func(t *testing.T) {
		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, cli.Attach(ctx, d1))
		defer func() { assert.NoError(t, cli.Detach(ctx, d1)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewArray("todos")
			return nil
		}, "create todos"))
		assert.Equal(t, `{"todos":[]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("todos").AddString("buy coffee")
			return nil
		}, "buy coffee"))
		assert.Equal(t, `{"todos":["buy coffee"]}`, d1.Marshal())

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetArray("todos").AddString("buy bread")
			return nil
		}, "buy bread"))
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, d1.Marshal())
		assert.NoError(t, cli.Sync(ctx))

		changes, err := adminCli.ListChangeSummaries(ctx, "default", d1.Key(), 0, 0, true)
		assert.NoError(t, err)
		// NOTE(chacha912): When attaching, a change is made to set the initial presence.
		assert.Len(t, changes, 4)

		assert.Equal(t, "create todos", changes[2].Message)
		assert.Equal(t, "buy coffee", changes[1].Message)
		assert.Equal(t, "buy bread", changes[0].Message)

		assert.Equal(t, `{"todos":[]}`, changes[2].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee"]}`, changes[1].Snapshot)
		assert.Equal(t, `{"todos":["buy coffee","buy bread"]}`, changes[0].Snapshot)
	})
}

func TestHistorySkippedUndo(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	t.Run("fully skipped undo propagates nothing test", func(t *testing.T) {
		// An undo whose every operation is skipped -- because a peer
		// concurrently removed the target -- must not become a change at
		// all. JS's Change.execute drops a skipped operation before it can
		// reach `changeOperations` (change.ts:174), so document.ts's
		// `!opInfos.length` early return fires and nothing is queued.
		//
		// If Go instead reports the skipped operation as executed, the
		// change is appended to localChanges and shipped. Peers then run it
		// under OpSourceRemote, where the skip guard does not apply, so
		// every replica executes an operation the originator skipped. The
		// content still converges -- but DocSize does not, and DocSize is
		// what gates MaxSizeLimit.
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1))
		defer func() { assert.NoError(t, c1.Detach(ctx, d1)) }()

		d2 := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, d2))
		defer func() { assert.NoError(t, c2.Detach(ctx, d2)) }()

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewObject("k")
			return nil
		}))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetObject("k").SetString("a", "1")
			return nil
		}))
		assert.Equal(t, `{"k":{"a":"1"}}`, d1.Marshal())
		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})

		// d2 removes the object the stacked undo targets, then d1 learns of
		// it. d1's undo now has nothing left to act on.
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.Delete("k")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, `{}`, d1.Marshal())
		assert.False(t, d1.HasLocalChanges())

		assert.True(t, d1.CanUndo())
		assert.NoError(t, d1.Undo())
		assert.Equal(t, `{}`, d1.Marshal())
		assert.False(t, d1.HasLocalChanges(), "a fully skipped undo must queue no change")

		syncClientsThenAssertEqual(t, []clientAndDocPair{{c1, d1}, {c2, d2}})
		assert.Equal(t, d1.DocSize(), d2.DocSize(), "DocSize must agree across replicas")
	})
}
