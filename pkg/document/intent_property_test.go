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

package document_test

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// intentSyncAll delivers every replica's pending changes to every other
// replica, then acknowledges each replica's own changes.
func intentSyncAll(t *testing.T, docs []*document.Document) {
	t.Helper()
	packs := make([]*change.Pack, len(docs))
	for i, d := range docs {
		packs[i] = d.CreateChangePack()
	}
	for i, p := range packs {
		for j, d := range docs {
			if i == j {
				continue
			}
			require.NoError(t, d.ApplyChangePack(change.NewPack(
				p.DocumentKey, change.NewCheckpoint(0, 0), p.Changes, time.InitialVersionVector, nil,
			)))
		}
	}
	for i, p := range packs {
		var lastSeq uint32
		if len(p.Changes) > 0 {
			lastSeq = p.Changes[len(p.Changes)-1].ClientSeq()
		}
		require.NoError(t, docs[i].ApplyChangePack(change.NewPack(
			p.DocumentKey, change.NewCheckpoint(0, lastSeq), nil, time.InitialVersionVector, nil,
		)))
	}
}

func intentReplicas(t *testing.T, n int, base string) []*document.Document {
	t.Helper()
	docs := make([]*document.Document, n)
	for i := range docs {
		a, err := time.ActorIDFromHex(fmt.Sprintf("%024x", i+1))
		require.NoError(t, err)
		docs[i] = document.New("intent-doc")
		docs[i].SetActor(a)
	}
	require.NoError(t, docs[0].Update(func(root *json.Object, _ *presence.Presence) error {
		root.SetNewText("t").Edit(0, 0, base)
		return nil
	}))
	intentSyncAll(t, docs)
	return docs
}

// TestIntentPropertyInterleaving measures how often concurrent runs of
// insertions interleave. Each replica types one run of 2-5 distinct
// characters, one Edit per character, starting either at the same index (5)
// or at a neighbouring one (4-6), and placing each character by a strategy:
//   - forward: after the previous character of its run,
//   - backward: at the run's start (the caret stays fixed),
//   - random: anywhere inside its own run so far,
//   - mixed: each replica picks one of the three.
//
// Every replica must converge; a trial counts as interleaved when any
// replica's run is not a contiguous substring of the merged text. The test
// fails only on divergence, or if forward typing ever interleaves.
func TestIntentPropertyInterleaving(t *testing.T) {
	const trials = 300
	alphabets := []string{"가나다라마바", "abcdef", "ABCDEF"}
	base := "0123456789"

	for _, n := range []int{2, 3} {
		for _, strategy := range []string{"forward", "backward", "random", "mixed"} {
			for _, neighbouring := range []bool{false, true} {
				placement := "same"
				if neighbouring {
					placement = "neighbouring"
				}
				name := fmt.Sprintf("replicas=%d strategy=%s index=%s", n, strategy, placement)
				t.Run(name, func(t *testing.T) {
					interleaved := 0
					for seed := range trials {
						rnd := rand.New(rand.NewSource(int64(seed + 1))) //nolint:gosec // reproducible seeds
						docs := intentReplicas(t, n, base)

						runs := make([]string, n)
						for i, d := range docs {
							start := 5
							if neighbouring {
								start = 4 + rnd.Intn(3)
							}
							s := strategy
							if s == "mixed" {
								s = []string{"forward", "backward", "random"}[rnd.Intn(3)]
							}
							chars := []rune(alphabets[i])[:2+rnd.Intn(4)]
							var run []rune
							for _, c := range chars {
								off := 0
								switch s {
								case "forward":
									off = len(run)
								case "random":
									off = rnd.Intn(len(run) + 1)
								}
								at := start + off
								require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
									root.GetText("t").Edit(at, at, string(c))
									return nil
								}))
								run = append(run[:off], append([]rune{c}, run[off:]...)...)
							}
							runs[i] = string(run)
						}

						intentSyncAll(t, docs)
						merged := docs[0].Root().GetText("t").String()
						for _, d := range docs[1:] {
							require.Equal(t, merged, d.Root().GetText("t").String(), "seed %d diverged", seed)
						}
						for _, r := range runs {
							if !strings.Contains(merged, r) {
								interleaved++
								break
							}
						}
					}
					t.Logf("STAT %s interleaved=%d/%d (%.1f%%)", name, interleaved, trials,
						100*float64(interleaved)/trials)
					if strategy == "forward" {
						require.Zero(t, interleaved, "forward typing must not interleave")
					}
				})
			}
		}
	}
}
