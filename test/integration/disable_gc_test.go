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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestDisableGCOnAttach(t *testing.T) {
	t.Run("opt-out client writes no versionvectors row", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		d1 := document.New(helper.TestKey(t))
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisableGC()))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0).Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		// Inspect server-side state to confirm the wire contract: no row
		// must exist in the versionvectors table for this client.
		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, d1.Key())
		assert.NoError(t, err)

		minVV, err := be.DB.GetMinVersionVector(ctx, docInfo.RefKey(), time.NewVersionVector())
		assert.NoError(t, err)
		assert.Len(t, minVV, 0,
			"opt-out client must not write to the versionvectors table")
	})

	t.Run("mixed opt-in and opt-out clients converge on counter value", func(t *testing.T) {
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		c1, c2 := clients[0], clients[1]

		ctx := context.Background()
		docKey := helper.TestKey(t)
		d1 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d1))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))

		d2 := document.New(docKey)
		assert.NoError(t, c2.Attach(ctx, d2, client.WithDisableGC()))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))

		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c1.Sync(ctx))

		assert.Equal(t, "2", d1.Root().GetCounter("counter").Marshal())
		assert.Equal(t, "2", d2.Root().GetCounter("counter").Marshal())
	})

	t.Run("opt-out clients keep per-Change VV at size 1 under multi-actor fanout", func(t *testing.T) {
		// Regression for the bug where Change.ID.VersionVector accumulated
		// O(num_actors) entries on every opt-out client because
		// ApplyChanges -> SyncClocks merged every remote actor into the
		// local VV. After the SyncLamport fix the per-Change VV must stay
		// at size 1 so the on-the-wire savings the opt-out promises
		// actually materialize.
		clients := activeClients(t, 3)
		defer deactivateAndCloseClients(t, clients)
		c1, c2, c3 := clients[0], clients[1], clients[2]

		ctx := context.Background()
		docKey := helper.TestKey(t)
		d1 := document.New(docKey)
		d2 := document.New(docKey)
		d3 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisableGC()))
		assert.NoError(t, c2.Attach(ctx, d2, client.WithDisableGC()))
		assert.NoError(t, c3.Attach(ctx, d3, client.WithDisableGC()))

		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c2.Sync(ctx))
		assert.NoError(t, c3.Sync(ctx))

		// A few rounds of mutual increments + sync. The second round is
		// when accumulation used to first show up because every actor has
		// then seen every other actor at least once.
		for range 3 {
			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter("counter").Increase(1)
				return nil
			}))
			assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter("counter").Increase(1)
				return nil
			}))
			assert.NoError(t, d3.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetCounter("counter").Increase(1)
				return nil
			}))
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c3.Sync(ctx))
		}

		// Drain remaining pushed changes so every client sees the full
		// counter. Two extra sync passes are enough for a 3-client mesh.
		for range 2 {
			assert.NoError(t, c1.Sync(ctx))
			assert.NoError(t, c2.Sync(ctx))
			assert.NoError(t, c3.Sync(ctx))
		}

		// Convergence sanity check: 3 rounds * 3 actors = 9 increments.
		assert.Equal(t, "9", d1.Root().GetCounter("counter").Marshal())
		assert.Equal(t, "9", d2.Root().GetCounter("counter").Marshal())
		assert.Equal(t, "9", d3.Root().GetCounter("counter").Marshal())

		// The contract assertion: every opt-out doc's VV stays at size 1.
		for i, d := range []*document.Document{d1, d2, d3} {
			vv := d.VersionVector()
			assert.Equal(t, 1, len(vv),
				"opt-out doc[%d].VersionVector() must stay at size 1, got %s",
				i, vv.Marshal())
		}

		// And so does the next produced local Change.
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		pack := d1.CreateChangePack()
		vv := pack.Changes[len(pack.Changes)-1].ID().VersionVector()
		assert.Equal(t, 1, len(vv),
			"new local Change produced under disable_gc must carry size-1 VV, got %s",
			vv.Marshal())
	})

	t.Run("re-attach without WithDisableGC restores normal GC participation", func(t *testing.T) {
		clients := activeClients(t, 1)
		defer deactivateAndCloseClients(t, clients)
		c1 := clients[0]

		ctx := context.Background()
		docKey := helper.TestKey(t)

		// First attach with opt-out.
		d1 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d1, client.WithDisableGC()))
		assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("counter", 0).Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.NoError(t, c1.Detach(ctx, d1))

		// Re-attach without opt-out. The SDK derives the per-request flag
		// from the current attachment only, so subsequent PushPulls
		// participate in minVV tracking normally.
		d2 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d2))
		assert.NoError(t, d2.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, c1.Sync(ctx))
		assert.Equal(t, "2", d2.Root().GetCounter("counter").Marshal())

		// Confirm server-side: re-attached client now appears in the
		// versionvectors table. The opt-out attach didn't write a row, so
		// a non-empty minVV after re-attach proves the new attachment is
		// participating in minVV tracking.
		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, d2.Key())
		assert.NoError(t, err)
		minVV, err := be.DB.GetMinVersionVector(ctx, docInfo.RefKey(), time.NewVersionVector())
		assert.NoError(t, err)
		assert.NotZero(t, len(minVV),
			"re-attach without opt-out should resume minVV tracking")
	})

	t.Run("opt-out client picks up server lamport when attach returns a snapshot", func(t *testing.T) {
		// Latent issue exposed by the snapshot pull path:
		// server/packs/pushpull.go nils resPack.VersionVector for opt-out
		// even when the response is a snapshot, leaving the client without
		// the lamport info it needs to catch up. Today this test FAILS
		// because the opt-out client's lamport remains ~1 after the
		// snapshot pull, while the server doc has advanced far beyond.
		clients := activeClients(t, 2)
		defer deactivateAndCloseClients(t, clients)
		c1, c2 := clients[0], clients[1]

		ctx := context.Background()
		docKey := helper.TestKey(t)

		// 01. c1 attaches normally and writes past the snapshot threshold
		// so that any later pull at serverSeq=0 produces a snapshot
		// response.
		d1 := document.New(docKey)
		assert.NoError(t, c1.Attach(ctx, d1))
		for i := 0; i <= int(helper.SnapshotThreshold); i++ {
			assert.NoError(t, d1.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetInteger(fmt.Sprintf("k%d", i), i)
				return nil
			}))
		}
		assert.NoError(t, c1.Sync(ctx))
		d1Lamport := d1.InternalDocument().Lamport()

		// 02. c2 attaches with opt-out. The attach response's pull path
		// crosses the threshold, so the server returns a snapshot. With
		// the current behavior the response's VV is nil and c2's lamport
		// fails to catch up.
		d2 := document.New(docKey)
		assert.NoError(t, c2.Attach(ctx, d2, client.WithDisableGC()))

		d2Lamport := d2.InternalDocument().Lamport()
		t.Logf("d1.lamport=%d  d2.lamport after opt-out snapshot pull=%d",
			d1Lamport, d2Lamport)

		assert.GreaterOrEqual(t, d2Lamport, d1Lamport,
			"opt-out client must catch up to the server's lamport via the "+
				"snapshot response; got %d, server is at %d",
			d2Lamport, d1Lamport)
	})

	t.Run("snapshot row stores empty VV when every client opts out", func(t *testing.T) {
		// Server-side regression: with no opt-in client tracking the doc
		// (no row in versionvectors), the persisted snapshot row's VV has
		// no GC consumer. CreateSnapshotInfo must therefore store an
		// empty VV so opt-out-only documents do not accumulate per-actor
		// entries in snapshot rows. Before the fix, every snapshot row
		// carried a VV of size O(num_actors_ever).
		const numClients = 5
		clients := activeClients(t, numClients)
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()
		docKey := helper.TestKey(t)
		docs := make([]*document.Document, numClients)
		for i := 0; i < numClients; i++ {
			docs[i] = document.New(docKey)
			assert.NoError(t, clients[i].Attach(ctx, docs[i], client.WithDisableGC()))
		}

		// c0 creates the counter; everyone increments enough rounds to
		// cross the test backend's snapshot threshold (10).
		assert.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("c", 0)
			return nil
		}))
		assert.NoError(t, clients[0].Sync(ctx))
		for i := 1; i < numClients; i++ {
			assert.NoError(t, clients[i].Sync(ctx))
		}
		for round := 0; round < 3; round++ {
			for i := 0; i < numClients; i++ {
				assert.NoError(t, docs[i].Update(func(root *json.Object, p *presence.Presence) error {
					root.GetCounter("c").Increase(1)
					return nil
				}))
				assert.NoError(t, clients[i].Sync(ctx))
			}
		}
		// Flush so storeSnapshot fires in the pubsub goroutine.
		for i := 0; i < numClients; i++ {
			assert.NoError(t, clients[i].Sync(ctx))
		}

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, docKey)
		assert.NoError(t, err)
		snapInfo, err := be.DB.FindClosestSnapshotInfo(
			ctx, docInfo.RefKey(), docInfo.ServerSeq, false,
		)
		assert.NoError(t, err)
		assert.NotEqual(t, int64(0), snapInfo.ServerSeq,
			"sanity: a snapshot row must exist past serverSeq 0")
		assert.Equal(t, 0, len(snapInfo.VersionVector),
			"opt-out-only snapshot row VV must be empty, got %s",
			snapInfo.VersionVector.Marshal())
	})

	t.Run("snapshot row preserves VV when at least one client is opt-in", func(t *testing.T) {
		// Counter test for the previous regression: as soon as a single
		// opt-in client is tracking the doc, the snapshot VV must be
		// preserved (with at least that client's entry) so the opt-in
		// client's GC path keeps working.
		clients := activeClients(t, 4)
		defer deactivateAndCloseClients(t, clients)

		ctx := context.Background()
		docKey := helper.TestKey(t)

		// clients[0] is opt-in; clients[1..3] are opt-out.
		docs := make([]*document.Document, 4)
		docs[0] = document.New(docKey)
		assert.NoError(t, clients[0].Attach(ctx, docs[0]))
		for i := 1; i < 4; i++ {
			docs[i] = document.New(docKey)
			assert.NoError(t, clients[i].Attach(ctx, docs[i], client.WithDisableGC()))
		}

		assert.NoError(t, docs[0].Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewCounter("c", 0)
			return nil
		}))
		for _, c := range clients {
			assert.NoError(t, c.Sync(ctx))
		}
		for round := 0; round < 3; round++ {
			for i := 0; i < 4; i++ {
				assert.NoError(t, docs[i].Update(func(root *json.Object, p *presence.Presence) error {
					root.GetCounter("c").Increase(1)
					return nil
				}))
				assert.NoError(t, clients[i].Sync(ctx))
			}
		}
		for _, c := range clients {
			assert.NoError(t, c.Sync(ctx))
		}

		be := defaultServer.Backend()
		project, err := defaultServer.DefaultProject(ctx)
		assert.NoError(t, err)
		docInfo, err := be.DB.FindDocInfoByKey(ctx, project.ID, docKey)
		assert.NoError(t, err)
		snapInfo, err := be.DB.FindClosestSnapshotInfo(
			ctx, docInfo.RefKey(), docInfo.ServerSeq, false,
		)
		assert.NoError(t, err)
		assert.NotEqual(t, int64(0), snapInfo.ServerSeq,
			"sanity: a snapshot row must exist past serverSeq 0")
		assert.NotZero(t, len(snapInfo.VersionVector),
			"mixed-mode snapshot row VV must be preserved (at least the "+
				"opt-in client's entry); got empty")
	})
}
