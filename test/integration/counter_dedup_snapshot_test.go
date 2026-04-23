//go:build integration

/*
 * Repro test: dedup counter + snapshot interaction.
 *
 * Exercises the background storeSnapshot / storeRevision paths that
 * process Documents containing IntegerDedupCnt. Two failure modes:
 *   1. storeRevision → yson.go Counter.Marshal lacks dedup case
 *      → "marshal counter: unsupported element"
 *   2. storeSnapshot → doc.ApplyChangePack replays ops on loaded snapshot
 *      → if the HLL state in the snapshot isn't restored correctly, the
 *        next Increase op fails → "not applicable datatype"
 *
 * Both variants produce a silent server-side error (background goroutine)
 * while client API returns success.
 */

package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// Variant 1: single client, many changes → triggers storeRevision path.
func TestCounterDedupSnapshotReproSingleClient(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()
	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))

	const actors = 15
	for i := 0; i < actors; i++ {
		actor := fmt.Sprintf("user-%d", i)
		err := d1.Update(func(root *json.Object, p *presence.Presence) error {
			if i == 0 {
				root.SetNewDedupCounter("uv").Add(actor)
			} else {
				root.GetCounter("uv").Add(actor)
			}
			return nil
		}, fmt.Sprintf("add %s", actor))
		assert.NoError(t, err)
	}
	assert.NoError(t, c1.Sync(ctx))

	time.Sleep(2 * time.Second)

	d2 := document.New(helper.TestKey(t))
	assert.NoError(t, c2.Attach(ctx, d2))
	assert.Equal(t, fmt.Sprintf(`{"uv":%d}`, actors), d2.Marshal())
}

// Variant 2: concurrent clients, each adding a distinct actor → triggers
// the storeSnapshot replay path that references HLL state.
func TestCounterDedupSnapshotReproConcurrent(t *testing.T) {
	const N = 4     // concurrent clients
	const perVu = 8 // each client adds 8 distinct actors
	clients := activeClients(t, N+1)
	verifier := clients[N]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()
	docKey := helper.TestKey(t)

	// Stage 1: one writer creates the dedup counter, syncs.
	seed := document.New(docKey)
	assert.NoError(t, clients[0].Attach(ctx, seed))
	assert.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewDedupCounter("uv").Add("seed")
		return nil
	}, "seed"))
	assert.NoError(t, clients[0].Sync(ctx))

	// Stage 2: N-1 more clients attach and concurrently add distinct actors.
	docs := make([]*document.Document, N)
	docs[0] = seed
	for i := 1; i < N; i++ {
		d := document.New(docKey)
		assert.NoError(t, clients[i].Attach(ctx, d))
		docs[i] = d
	}

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(vu int) {
			defer wg.Done()
			for j := 0; j < perVu; j++ {
				actor := fmt.Sprintf("user-%d-%d", vu, j)
				err := docs[vu].Update(func(root *json.Object, p *presence.Presence) error {
					root.GetCounter("uv").Add(actor)
					return nil
				}, actor)
				assert.NoError(t, err)
				assert.NoError(t, clients[vu].Sync(ctx))
			}
		}(i)
	}
	wg.Wait()

	// Stage 3: wait for background snapshot/revision goroutines to run.
	time.Sleep(3 * time.Second)

	// Stage 4: fresh client attaches — exercises server snapshot load/replay.
	vDoc := document.New(docKey)
	assert.NoError(t, verifier.Attach(ctx, vDoc))

	// Expected = 1 (seed) + N*perVu distinct actors = 1 + 4*8 = 33
	expected := 1 + N*perVu
	assert.Equal(t, fmt.Sprintf(`{"uv":%d}`, expected), vDoc.Marshal())
}
