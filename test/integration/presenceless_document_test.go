//go:build integration

/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestPresencelessDocument(t *testing.T) {
	t.Run("first attach fixates disable_presence on DocInfo", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisablePresence()))

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
		assert.NoError(t, err)
		assert.True(t, docInfo.DisablePresence,
			"first attach with WithDisablePresence should fixate the flag on DocInfo")
	})

	t.Run("late attacher observes the persisted value", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		c1, c2 := clients[0], clients[1]

		ctx := context.Background()
		docKey := helper.TestKey(t)
		d1 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisablePresence()))

		// Second client attaches without the option but should observe the
		// server-fixated true. The local Document.options.DisablePresence
		// is updated from the response so subsequent Updates gate.
		d2 := document.New(docKey)
		assert.NoError(t, c2.Attach(ctx, d2))

		assert.NoError(t, d2.Update(func(_ *json.Object, p *presence.Presence) error {
			p.Set("name", "leaker")
			return nil
		}))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		// No presence entry should be visible from either side.
		assert.Empty(t, d1.AllPresences(), "doc1 should not see d2's presence")
		assert.Empty(t, d2.AllPresences(), "doc2 should have presence gated out locally")
	})

	t.Run("PUT presence is stripped on PushPull", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisablePresence()))

		// Counter-style mutation to ensure the doc has at least one
		// operation and produces a non-presence change.
		assert.NoError(t, d1.Update(func(root *json.Object, _ *presence.Presence) error {
			root.SetNewCounter("counter", 0).Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		assert.Empty(t, d1.AllPresences(),
			"presenceless doc must carry an empty presence map after sync")
	})

	t.Run("backward compatibility — no option keeps presence path live", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithPresence(presence.Data{"role": "owner"})))

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
		assert.NoError(t, err)
		assert.False(t, docInfo.DisablePresence,
			"omitting the option should leave DocInfo on the default false")
		assert.NotEmpty(t, d1.AllPresences(),
			"default-path doc should still accumulate presence")
	})

	t.Run("force-exit clients do not leak into a late attacher's response", func(t *testing.T) {
		// Regression for the original symptom that motivated this option:
		// dozens of thousands of clients attach to a Counter-only document
		// and never call Detach (mobile background, iOS Safari, network
		// drop). Without the option the document's presence map grows
		// without bound, inflating the AttachDocument response for every
		// subsequent client. With the option on, the presence map must
		// stay empty no matter how many earlier clients walked away.
		const N = 5

		ctx := context.Background()
		docKey := helper.TestKey(t)

		// Spawn N "force-exit" clients: each attaches with the option,
		// sets a presence entry, syncs, and then is dropped without ever
		// calling Detach or Deactivate. The server-side strip is the only
		// thing keeping their entries out of the document.
		forceClients := activeClients(t, N)
		// NOTE: intentionally not calling deactivateAndCloseClients on
		// forceClients — the test models clients that disappeared
		// ungracefully. We do close the connections at the end so the
		// goroutines exit cleanly, but no Deactivate.
		defer func() {
			for _, c := range forceClients {
				assert.NoError(t, c.Close())
			}
		}()

		for i, c := range forceClients {
			d := document.New(docKey)
			assert.NoError(t, c.Attach(ctx, d, client.WithDisablePresence()))
			actor := i // capture
			assert.NoError(t, d.Update(func(_ *json.Object, p *presence.Presence) error {
				p.Set("idx", strconv.Itoa(actor))
				return nil
			}))
			assert.NoError(t, c.Sync(ctx))
		}

		// A fresh client now attaches without opting out. Even though the
		// previous N clients all wrote presence and none of them cleaned
		// up, the late attacher's view must carry no foreign presence.
		measureClient := activeClients(t, 1)
		defer deactivateAndCloseClients(t, measureClient)
		dm := document.New(docKey)
		assert.NoError(t, measureClient[0].Attach(ctx, dm))

		// Filter out the measure client's own actor — if any presence
		// remains it must only be its own self-entry, never one of the
		// force-exited writers.
		for actorID := range dm.AllPresences() {
			assert.Equal(t, measureClient[0].ID().String(), actorID,
				"presenceless doc must surface no foreign presence even when prior clients force-exited")
		}
	})

	t.Run("force-exit clients do not leak when the late attacher pulls a snapshot", func(t *testing.T) {
		// Same regression as above but driven across the snapshot
		// threshold (helper.SnapshotThreshold = 10) so the measuring
		// client's attach response goes through pullSnapshot rather than
		// pullChanges. This exercises both the entry-side strip and the
		// snapshot-time ResetPresences/empty-map serialization at once.
		const N = 5
		const updatesPerClient = 3 // 5 × 3 = 15 > SnapshotThreshold(10)

		ctx := context.Background()
		docKey := helper.TestKey(t)

		forceClients := activeClients(t, N)
		defer func() {
			for _, c := range forceClients {
				assert.NoError(t, c.Close())
			}
		}()

		// Each Update touches the root (Counter increment) and presence —
		// the same shape the symptom workload uses. After strip the
		// operation half remains, so server_seq grows; if we set presence
		// alone the entry-side strip would drop the whole change and
		// server_seq would stay at zero, never crossing the snapshot
		// threshold.
		for i, c := range forceClients {
			d := document.New(docKey)
			assert.NoError(t, c.Attach(ctx, d, client.WithDisablePresence()))
			actor := i // capture
			for round := range updatesPerClient {
				r := round // capture
				assert.NoError(t, d.Update(func(root *json.Object, p *presence.Presence) error {
					if root.Get("counter") == nil {
						root.SetNewCounter("counter", int64(0))
					}
					root.GetCounter("counter").Increase(1)
					p.Set("idx", strconv.Itoa(actor))
					p.Set("round", strconv.Itoa(r))
					return nil
				}))
				assert.NoError(t, c.Sync(ctx))
			}
			// intentional: no detach / deactivate
		}

		// Sanity-check the threshold was actually crossed so the late
		// attacher really pulls a snapshot — otherwise the test is the
		// same as the previous (changes-path) case.
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := defaultServer.Backend().DB.FindDocInfoByKey(ctx, project.ID, docKey)
		assert.NoError(t, err)
		assert.Greater(t, docInfo.ServerSeq, helper.SnapshotThreshold,
			"prerequisite: server_seq must exceed SnapshotThreshold so the late attach hits pullSnapshot")

		measureClient := activeClients(t, 1)
		defer deactivateAndCloseClients(t, measureClient)
		dm := document.New(docKey)
		assert.NoError(t, measureClient[0].Attach(ctx, dm))

		for actorID := range dm.AllPresences() {
			assert.Equal(t, measureClient[0].ID().String(), actorID,
				"presenceless doc must surface no foreign presence even after the snapshot path")
		}
	})
}
