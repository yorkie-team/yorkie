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

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	// objectPropertyClientCount is the number of replicas that edit the
	// document concurrently.
	objectPropertyClientCount = 3

	// objectPropertySeedCount is the number of independent random sequences
	// to run.
	objectPropertySeedCount = 10

	// objectPropertyOpCount is the number of operations each client applies
	// before synchronization.
	objectPropertyOpCount = 8

	// objectPropertyKeyRange is intentionally narrow. A small key space makes
	// the clients touch the same key often, which is where convergence is at
	// risk.
	objectPropertyKeyRange = 3
)

// applyRandomObjectOps applies a randomly generated sequence of Object
// operations to the given document. The sequence is fully determined by r, so
// a failing case can be reproduced from its seed.
func applyRandomObjectOps(t *testing.T, doc *document.Document, r *rand.Rand, opCount int) {
	for range opCount {
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			key := fmt.Sprintf("k%d", r.Intn(objectPropertyKeyRange))

			switch r.Intn(7) {
			case 0:
				root.SetString(key, fmt.Sprintf("v%d", r.Intn(100)))
			case 1:
				root.SetInteger(key, r.Intn(100))
			case 2:
				root.SetLong(key, int64(r.Intn(100)))
			case 3:
				root.SetDouble(key, r.Float64())
			case 4:
				root.SetBool(key, r.Intn(2) == 0)
			case 5:
				root.SetNull(key)
			case 6:
				root.Delete(key)
			}

			return nil
		}))
	}
}

// TestObjectPropertyConvergence verifies that every replica converges to the
// same state after applying randomly generated Object operation sequences,
// rather than the hand-written scenarios object_test.go relies on.
func TestObjectPropertyConvergence(t *testing.T) {
	clients := activeClients(t, objectPropertyClientCount)
	defer deactivateAndCloseClients(t, clients)

	for seed := range objectPropertySeedCount {
		t.Run(fmt.Sprintf("object converges with seed %d", seed), func(t *testing.T) {
			ctx := context.Background()

			var pairs []clientAndDocPair
			for _, cli := range clients {
				doc := document.New(helper.TestKey(t))
				assert.NoError(t, cli.Attach(ctx, doc))
				pairs = append(pairs, clientAndDocPair{cli, doc})
			}

			// Every replica gets its own seed so that the sequences differ
			// while each one stays reproducible.
			for i, pair := range pairs {
				//nolint:gosec // A weak PRNG is intended: the seed reproduces failures.
				r := rand.New(rand.NewSource(int64(seed*objectPropertyClientCount + i)))
				applyRandomObjectOps(t, pair.doc, r, objectPropertyOpCount)
			}

			syncClientsThenAssertEqual(t, pairs)
		})
	}
}
