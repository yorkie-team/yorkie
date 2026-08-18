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
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

const (
	// textPropertyClientCount is the number of replicas that edit the
	// document concurrently.
	textPropertyClientCount = 3

	// textPropertySeedCount is the number of independent random sequences
	// to run.
	textPropertySeedCount = 10

	// textPropertyOpCount is the number of operations each client applies
	// before synchronization.
	textPropertyOpCount = 8

	// textPropertyKey is the key of the shared Text under the root object.
	textPropertyKey = "text"
)

// randomTextContent returns a short random ASCII string. An empty string turns
// the edit into a deletion, and using ASCII keeps one character equal to one
// UTF-16 code unit so the tracked length stays exact.
func randomTextContent(r *rand.Rand) string {
	b := make([]byte, r.Intn(4))
	for i := range b {
		b[i] = byte('a' + r.Intn(26))
	}
	return string(b)
}

// applyRandomTextOps applies a randomly generated sequence of Text operations
// to the given document. The sequence is fully determined by r, so a failing
// case can be reproduced from its seed.
func applyRandomTextOps(t *testing.T, doc *document.Document, r *rand.Rand, opCount int) {
	// length tracks the replica's local text length (in UTF-16 units) so the
	// randomly picked ranges always stay in bounds.
	length := 0
	for range opCount {
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			text := root.GetText(textPropertyKey)

			switch r.Intn(3) {
			case 0, 1:
				// Edit: insert, replace, or delete over a narrow range. A narrow
				// range makes the replicas touch the same region often, which is
				// where text convergence is at risk.
				from := r.Intn(length + 1)
				to := from + r.Intn(length-from+1)
				content := randomTextContent(r)
				text.Edit(from, to, content)
				length += len(content) - (to - from)
			case 2:
				// Style an existing non-empty range.
				if length > 0 {
					from := r.Intn(length)
					to := from + 1 + r.Intn(length-from)
					text.Style(from, to, map[string]string{"bold": strconv.Itoa(r.Intn(2))})
				}
			}

			return nil
		}))
	}
}

// TestTextPropertyConvergence verifies that every replica converges to the same
// state after applying randomly generated Text operation sequences, rather than
// the hand-written scenarios text_test.go relies on.
func TestTextPropertyConvergence(t *testing.T) {
	clients := activeClients(t, textPropertyClientCount)
	defer deactivateAndCloseClients(t, clients)

	for seed := range textPropertySeedCount {
		t.Run(fmt.Sprintf("text converges with seed %d", seed), func(t *testing.T) {
			ctx := context.Background()

			var pairs []clientAndDocPair
			for _, cli := range clients {
				doc := document.New(helper.TestKey(t))
				assert.NoError(t, cli.Attach(ctx, doc))
				pairs = append(pairs, clientAndDocPair{cli, doc})
			}

			// Create the shared Text on one replica and propagate it, so every
			// replica edits the same CRDT. A per-replica SetNewText would create
			// distinct texts that resolve by last-writer-wins and drop edits.
			assert.NoError(t, pairs[0].doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText(textPropertyKey)
				return nil
			}))
			syncClientsThenAssertEqual(t, pairs)

			// Every replica gets its own seed so that the sequences differ while
			// each one stays reproducible.
			for i, pair := range pairs {
				//nolint:gosec // A weak PRNG is intended: the seed reproduces failures.
				r := rand.New(rand.NewSource(int64(seed*textPropertyClientCount + i)))
				applyRandomTextOps(t, pair.doc, r, textPropertyOpCount)
			}

			syncClientsThenAssertEqual(t, pairs)
		})
	}
}
