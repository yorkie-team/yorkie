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
	"math/rand"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	// arrayPropertyClientCount is the number of replicas that edit the
	// document concurrently.
	arrayPropertyClientCount = 3

	// arrayPropertySeedCount is the number of independent random sequences
	// to run.
	arrayPropertySeedCount = 20

	// arrayPropertyOpCount is the number of operations each client applies
	// before synchronization.
	arrayPropertyOpCount = 8
)

// applyRandomArrayOps applies a randomly generated sequence of Array operations
// to the given document. The sequence is fully determined by r, so a failing
// case can be reproduced from its seed. Index-based ops (Insert/Delete/Set)
// read the replica's local length so the randomly picked indices always stay in
// bounds.
//
// NOTE(convergence scope): The generator covers Add, Insert, Delete, Set, and
// both Move variants (MoveBefore/MoveAfterByIndex). Concurrent Move used to be
// excluded here because randomized moves diverged (#1948); that divergence was a
// snapshot/DeepCopy reconstruction bug (Array.Add anchored on element identity
// instead of position-node identity) and is now fixed, so Move is exercised as a
// first-class convergence case.
func applyRandomArrayOps(t *testing.T, doc *document.Document, r *rand.Rand, opCount int) {
	for range opCount {
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			arr := root.GetArray("arr")
			length := arr.Len()

			switch r.Intn(6) {
			case 0:
				// Add: append a value to the end of the array.
				arr.AddInteger(r.Intn(100))
			case 1:
				// Insert: place a value after a random index. A narrow array
				// makes the replicas touch the same positions often, which is
				// where convergence is at risk. Falls back to Add when empty.
				if length == 0 {
					arr.AddInteger(r.Intn(100))
				} else {
					arr.InsertIntegerAfter(r.Intn(length), r.Intn(100))
				}
			case 2:
				// Delete: remove the element at a random index.
				if length > 0 {
					arr.Delete(r.Intn(length))
				}
			case 3:
				// Set: replace the element at a random index in place.
				if length > 0 {
					arr.SetInteger(r.Intn(length), r.Intn(100))
				}
			case 4:
				// MoveAfterByIndex: relocate an element after another index.
				if length >= 2 {
					pi := r.Intn(length)
					ti := r.Intn(length)
					if pi != ti {
						arr.MoveAfterByIndex(pi, ti)
					}
				}
			case 5:
				// MoveBefore: relocate an element before another element.
				if length >= 2 {
					ni := r.Intn(length)
					ti := r.Intn(length)
					if ni != ti {
						arr.MoveBefore(arr.Get(ni).CreatedAt(), arr.Get(ti).CreatedAt())
					}
				}
			}

			return nil
		}))
	}
}

// TestArrayPropertyConvergence verifies that every replica converges to the same
// state after applying randomly generated Array operation sequences, rather than
// the hand-written scenarios array_test.go relies on.
func TestArrayPropertyConvergence(t *testing.T) {
	clients := activeClients(t, arrayPropertyClientCount)
	defer deactivateAndCloseClients(t, clients)

	for seed := range arrayPropertySeedCount {
		t.Run(fmt.Sprintf("array converges with seed %d", seed), func(t *testing.T) {
			ctx := context.Background()

			var pairs []clientAndDocPair
			for _, cli := range clients {
				doc := document.New(helper.TestKey(t))
				require.NoError(t, cli.Attach(ctx, doc))
				pairs = append(pairs, clientAndDocPair{cli, doc})
			}

			// Create the shared Array on one replica and propagate it, so every
			// replica edits the same CRDT. A per-replica SetNewArray would create
			// distinct arrays that resolve by last-writer-wins and drop
			// operations.
			require.NoError(t, pairs[0].doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewArray("arr")
				return nil
			}))
			syncClientsThenAssertEqual(t, pairs)

			// Every replica gets its own seed so that the sequences differ while
			// each one stays reproducible.
			for i, pair := range pairs {
				//nolint:gosec // A weak PRNG is intended: the seed reproduces failures.
				r := rand.New(rand.NewSource(int64(seed*arrayPropertyClientCount + i)))
				applyRandomArrayOps(t, pair.doc, r, arrayPropertyOpCount)
			}

			syncClientsThenAssertEqual(t, pairs)
		})
	}
}
