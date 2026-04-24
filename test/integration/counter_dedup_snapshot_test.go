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

// Regression tests for dedup counter + snapshot/revision interaction (#1773).
//
// Exercises the background storeSnapshot / storeRevision paths that
// process documents containing IntegerDedupCnt via YSON marshaling.

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

// TestCounterDedupSnapshotSingleClient triggers storeRevision by
// exceeding SnapshotThreshold with a single client, then verifies a
// fresh client can attach and see the correct state.
func TestCounterDedupSnapshotSingleClient(t *testing.T) {
	clients := activeClients(t, 2)
	c1, c2 := clients[0], clients[1]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()
	d1 := document.New(helper.TestKey(t))
	assert.NoError(t, c1.Attach(ctx, d1))

	const actors = 15 // > helper.SnapshotThreshold (10)
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

	// Let the background snapshot/revision goroutine run.
	time.Sleep(2 * time.Second)

	d2 := document.New(helper.TestKey(t))
	assert.NoError(t, c2.Attach(ctx, d2))
	assert.Equal(t, fmt.Sprintf(`{"uv":%d}`, actors), d2.Marshal())
}

// TestCounterDedupSnapshotConcurrent exercises the background snapshot
// path under contention with multiple clients adding distinct actors.
func TestCounterDedupSnapshotConcurrent(t *testing.T) {
	const numClients = 4
	const perClient = 8
	clients := activeClients(t, numClients+1)
	verifier := clients[numClients]
	defer deactivateAndCloseClients(t, clients)

	ctx := context.Background()
	docKey := helper.TestKey(t)

	// One writer creates the dedup counter.
	seed := document.New(docKey)
	assert.NoError(t, clients[0].Attach(ctx, seed))
	assert.NoError(t, seed.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewDedupCounter("uv").Add("seed")
		return nil
	}, "seed"))
	assert.NoError(t, clients[0].Sync(ctx))

	// Remaining clients attach and concurrently add distinct actors.
	docs := make([]*document.Document, numClients)
	docs[0] = seed
	for i := 1; i < numClients; i++ {
		d := document.New(docKey)
		assert.NoError(t, clients[i].Attach(ctx, d))
		docs[i] = d
	}

	var wg sync.WaitGroup
	for i := 0; i < numClients; i++ {
		wg.Add(1)
		go func(vu int) {
			defer wg.Done()
			for j := 0; j < perClient; j++ {
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

	// Let background snapshot/revision goroutines run.
	time.Sleep(3 * time.Second)

	// Fresh client attaches — exercises server snapshot load/replay.
	vDoc := document.New(docKey)
	assert.NoError(t, verifier.Attach(ctx, vDoc))

	// Expected = 1 (seed) + numClients*perClient distinct actors.
	expected := 1 + numClients*perClient
	assert.Equal(t, fmt.Sprintf(`{"uv":%d}`, expected), vDoc.Marshal())
}
