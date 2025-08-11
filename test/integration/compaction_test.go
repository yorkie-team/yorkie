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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDocumentCompaction(t *testing.T) {
	clients := activeClients(t, 3)
	c1, c2, c3 := clients[0], clients[1], clients[2]
	defer deactivateAndCloseClients(t, clients)

	t.Run("text compaction test", func(t *testing.T) {
		ctx := context.Background()
		snapshotThreshold := int(helper.TestConfig().Backend.SnapshotThreshold)

		// 1. Create a document
		d1 := document.New(helper.TestDocKey(t))
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
		assert.NoError(t, defaultServer.CompactDocument(ctx, d1.Key()))

		// 3. Create another changes to create a snapshot
		docB := document.New(helper.TestDocKey(t))
		assert.NoError(t, c2.Attach(ctx, docB))
		for i := 0; i < snapshotThreshold; i++ {
			assert.NoError(t, docB.Update(func(r *json.Object, p *presence.Presence) error {
				text := r.GetText("text")
				currentLength := len(text.String())
				text.Edit(currentLength, currentLength, "x")
				return nil
			}))
		}
		assert.NoError(t, c2.Sync(ctx))

		// 4. Attach the document and try to delete its contents
		docC := document.New(helper.TestDocKey(t))
		assert.NoError(t, c3.Attach(ctx, docC))
		assert.NoError(t, docC.Update(func(r *json.Object, p *presence.Presence) error {
			r.GetText("text").Edit(7, 7+snapshotThreshold, "")
			return nil
		}))
		assert.Equal(t, `[{"val":"initial"}]`, docC.Root().GetText("text").Marshal())
	})
}
