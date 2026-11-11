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
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
)

// TestSkeletonGCMinRepro is the smallest divergence physical purge caused
// under the immediate right origin: base "XY", d0 deletes X and syncs, d1
// deletes Y, only d0 collects X, sync, then d0 types "a","b" and d1 types "c"
// at the front. With X unlinked on d0 only, d0 read "abc" and d1 "cab".
func TestSkeletonGCMinRepro(t *testing.T) {
	docs := intentReplicas(t, 2, "XY")
	d0, d1 := docs[0], docs[1]

	originEdit(t, d0, func(tx *json.Text) { tx.Edit(0, 1, "") })
	require.NoError(t, originSyncAll(docs))

	originEdit(t, d1, func(tx *json.Text) { tx.Edit(0, 1, "") })
	d0.GarbageCollect(originMinVV(docs))
	require.NoError(t, originSyncAll(docs))

	originEdit(t, d0, func(tx *json.Text) { tx.Edit(0, 0, "a") })
	originEdit(t, d0, func(tx *json.Text) { tx.Edit(1, 1, "b") })
	originEdit(t, d1, func(tx *json.Text) { tx.Edit(0, 0, "c") })
	require.NoError(t, originSyncAll(docs))

	require.Equal(t, intentText(d0), intentText(d1))
	skeletons := 0
	for _, n := range d0.Root().GetText("t").Nodes() {
		if n.IsSkeleton() {
			skeletons++
		}
	}
	require.Equal(t, 1, skeletons, "X is kept as a skeleton on d0")
}

// originReload replaces d's root with a decode of its own snapshot, the way a
// client that attaches late (or reloads) gets the document.
func originReload(d *document.Document) error {
	bytes, err := converter.SnapshotToBytes(d.RootObject(), nil)
	if err != nil {
		return err
	}
	return d.ApplyChangePack(change.NewPack(
		d.Key(), change.NewCheckpoint(0, 0), nil, d.VersionVector(), bytes,
	))
}

// TestSkeletonGCFuzz is TestTextOriginFuzz with GC and snapshot reloads, as an
// assertion: no divergence and no unresolved origin. ORIGIN_TRIALS raises the
// count (the report uses 20000).
func TestSkeletonGCFuzz(t *testing.T) {
	trials := 2000
	if v := os.Getenv("ORIGIN_TRIALS"); v != "" {
		trials, _ = strconv.Atoi(v)
	}
	diverged, errs, skeletonsSeen := 0, 0, 0
	var first string
	for seed := 0; seed < trials; seed++ {
		rnd := rand.New(rand.NewSource(int64(seed)))
		n := 2 + rnd.Intn(2)
		docs := intentReplicas(t, n, "XY")
		var trace []string
		ch := 'a'
		fail := func(err error) {
			errs++
			if first == "" {
				first = fmt.Sprintf("seed=%d err=%v\n  %s", seed, err, strings.Join(trace, "\n  "))
			}
		}
		ok := func() bool {
			rounds := 1 + rnd.Intn(4)
			for round := range rounds {
				for i, d := range docs {
					for k := rnd.Intn(3); k > 0; k-- {
						l := len([]rune(intentText(d)))
						if l > 0 && rnd.Intn(3) == 0 {
							at := rnd.Intn(l)
							w := 1 + rnd.Intn(min(2, l-at))
							trace = append(trace, fmt.Sprintf("r%d d%d delete %d..%d", round, i, at, at+w))
							originEdit(t, d, func(tx *json.Text) { tx.Edit(at, at+w, "") })
						} else {
							at := rnd.Intn(l + 1)
							c := string(ch)
							ch++
							trace = append(trace, fmt.Sprintf("r%d d%d insert %q at %d", round, i, c, at))
							originEdit(t, d, func(tx *json.Text) { tx.Edit(at, at, c) })
						}
					}
				}
				if rnd.Intn(2) == 0 {
					vv := originMinVV(docs)
					for i, d := range docs {
						if rnd.Intn(2) == 0 {
							trace = append(trace, fmt.Sprintf("r%d GC d%d", round, i))
							d.GarbageCollect(vv)
						}
					}
				}
				trace = append(trace, fmt.Sprintf("r%d sync", round))
				if err := originSyncAll(docs); err != nil {
					fail(err)
					return false
				}
				// After a sync nothing is pending, so a replica can be
				// rebuilt from its own snapshot.
				if rnd.Intn(3) == 0 {
					i := rnd.Intn(n)
					trace = append(trace, fmt.Sprintf("r%d reload d%d", round, i))
					if err := originReload(docs[i]); err != nil {
						fail(err)
						return false
					}
				}
			}
			return true
		}()
		if !ok {
			continue
		}
		for _, d := range docs {
			if intentText(d) != intentText(docs[0]) {
				diverged++
				if first == "" {
					first = fmt.Sprintf("seed=%d diverged\n  %s", seed, strings.Join(trace, "\n  "))
				}
				break
			}
		}
		for _, nd := range docs[0].Root().GetText("t").Nodes() {
			if nd.IsSkeleton() {
				skeletonsSeen++
			}
		}
	}
	t.Logf("SKELFUZZ diverged=%d errors=%d of %d (skeletons on d0 across trials: %d)", diverged, errs, trials, skeletonsSeen)
	require.Zero(t, diverged, first)
	require.Zero(t, errs, first)
	require.Positive(t, skeletonsSeen)
}

// TestSkeletonSnapshotRoundTrip: a collected text encodes its skeletons and
// decodes to the same structure, and the decoded copy keeps no GC pairs.
func TestSkeletonSnapshotRoundTrip(t *testing.T) {
	docs := intentReplicas(t, 2, "0123456789")
	originEdit(t, docs[0], func(tx *json.Text) { tx.Edit(2, 5, "") })
	originEdit(t, docs[1], func(tx *json.Text) { tx.Edit(7, 9, "ab") })
	require.NoError(t, originSyncAll(docs))
	docs[0].GarbageCollect(originMinVV(docs))
	require.Zero(t, docs[0].GarbageLen())

	want := docs[0].Root().GetText("t").ToTestString()
	require.Contains(t, want, "skel(")

	bytes, err := converter.ObjectToBytes(docs[0].RootObject())
	require.NoError(t, err)
	obj, err := converter.BytesToObject(bytes)
	require.NoError(t, err)
	got := obj.Get("t").(*crdt.Text)
	require.Equal(t, want, got.ToTestString())
	require.Empty(t, got.GCPairs())

	require.NoError(t, originReload(docs[0]))
	require.Zero(t, docs[0].GarbageLen())
	require.Equal(t, want, docs[0].Root().GetText("t").ToTestString())
}

// TestSkeletonUndoAfterGC: undoing a deletion whose tombstone was collected
// refills the skeleton in place.
func TestSkeletonUndoAfterGC(t *testing.T) {
	docs := intentReplicas(t, 2, "XabcY")
	d0 := docs[0]
	originEdit(t, d0, func(tx *json.Text) { tx.Edit(1, 4, "") })
	require.NoError(t, originSyncAll(docs))
	d0.GarbageCollect(originMinVV(docs))
	require.Contains(t, d0.Root().GetText("t").ToTestString(), "skel(3)")

	require.NoError(t, d0.Undo())
	require.Equal(t, "XabcY", intentText(d0))
	require.NotContains(t, d0.Root().GetText("t").ToTestString(), "skel(")
	require.NoError(t, originSyncAll(docs))
	require.Equal(t, intentText(d0), intentText(docs[1]))
}
