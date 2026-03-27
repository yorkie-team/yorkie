//go:build integration

/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocumentCompaction(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("text compaction test", func(t *testing.T) {
		ctx := context.Background()

		// 1. Create a document
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithInitialRoot(
			yson.ParseObject(`{"text": Text()}`),
		)))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "initial")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c1.Detach(ctx, d1))

		// 2. Compact the document
		assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), false))

		// 3. Create another changes to create a snapshot
		docB := document.New(helper.TestKey(t))
		assert.NoError(t, c2.Attach(ctx, docB))
		for i := 0; i < int(helper.SnapshotThreshold); i++ {
			assert.NoError(t, docB.Update(func(r *json.Object, p *presence.Presence) error {
				text := r.GetText("text")
				currentLength := len(text.String())
				text.Edit(currentLength, currentLength, "x")
				return nil
			}))
		}
		assert.NoError(t, c2.Sync(ctx))

		// 4. Attach the document and try to delete its contents
		docC := document.New(helper.TestKey(t))
		assert.NoError(t, c3.Attach(ctx, docC))
		assert.NoError(t, docC.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, len("initial"), "")
			return nil
		}))

		cloneRootText := docC.Root().GetText("text").Marshal()
		rootText := docC.InternalDocument().RootObject().Get("text").Marshal()
		assert.Equal(t, len(cloneRootText), len(rootText))
	})

	t.Run("force compaction on attached document test", func(t *testing.T) {
		ctx := context.Background()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithInitialRoot(
			yson.ParseObject(`{"text": Text()}`),
		)))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "hello")
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		// Without force: should fail because document is attached.
		// server.go calls packs.Compact directly (not via cluster service),
		// so ErrDocumentAttached is returned as-is.
		err := defaultServer.CompactDocument(ctx, d1.Key(), false)
		assert.Error(t, err)
		assert.True(t, errors.Is(err, packs.ErrDocumentAttached))

		// Force compact while attached: should succeed
		assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), true))

		// NOTE: After force compaction, the server resets serverSeq to 1,
		// but the client's checkpoint still references the old serverSeq.
		// This causes a mismatch on detach, which is an expected trade-off
		// of force compaction on attached documents.
		assert.Error(t, c1.Detach(ctx, d1))
	})

	t.Run("client recovers from epoch mismatch by reattaching with new client", func(t *testing.T) {
		ctx := context.Background()

		// Use a dedicated client to avoid interference from stale attachments
		// left by previous subtests (e.g. force compaction test leaves c1 with
		// an epoch-mismatched attachment that would poison c1.Sync calls here).
		cOld, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		assert.NoError(t, cOld.Activate(ctx))
		defer func() {
			assert.NoError(t, cOld.Deactivate(ctx))
			assert.NoError(t, cOld.Close())
		}()

		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, cOld.Attach(ctx, d1, client.WithInitialRoot(
			yson.ParseObject(`{"text": Text()}`),
		)))
		assert.NoError(t, d1.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(0, 0, "hello")
			return nil
		}))
		assert.NoError(t, cOld.Sync(ctx))

		// Force compact while client is attached
		assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key(), true))

		// Sync fails with epoch mismatch: the client's stored epoch no longer
		// matches the document epoch incremented by compaction.
		err = cOld.Sync(ctx)
		assert.Error(t, err)

		// Recovery: create a fresh client, activate it, and attach to the same
		// document key. The new client has no prior attachment record so it
		// goes through a clean attach and receives the compacted state.
		cNew, err := client.Dial(defaultServer.RPCAddr())
		assert.NoError(t, err)
		defer func() {
			assert.NoError(t, cNew.Deactivate(ctx))
			assert.NoError(t, cNew.Close())
		}()
		assert.NoError(t, cNew.Activate(ctx))

		d2 := document.New(d1.Key())
		assert.NoError(t, cNew.Attach(ctx, d2))

		// Verify the reattached document has the compacted content
		assert.Equal(t, `hello`, d2.Root().GetText("text").String())

		// Further edits work normally
		assert.NoError(t, d2.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(5, 5, " world")
			return nil
		}))
		assert.NoError(t, cNew.Sync(ctx))
	})
}
