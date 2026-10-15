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

// Why: issue #1957 asks whether the default SnapshotThreshold (500) is a
// good choice. When a client lags behind, the server either replays the
// missing changes or sends a snapshot. This benchmark provides data points
// for that trade-off (rebuild cost and response size versus the gap). It
// makes no recommendation for the default value.
//
// This file benchmarks how the distance between a stored snapshot and the
// requested server sequence (the "gap") affects the cost of rebuilding a
// document, and compares it with the wire size of the two response forms
// (a changes pack and a snapshot pack). It relates to the question of what
// the default SnapshotThreshold should be.
//
// What is measured:
//
//   - ns/op, B/op and allocs/op cover only packs.BuildInternalDocForServerSeq.
//     Cache purging and priming are done with the timer stopped. The call
//     loads the closest stored snapshot (or uses the cached one), replays
//     the changes up to the target server sequence and runs garbage
//     collection inside it.
//   - chg-*-B and snap-*-B report the size of a ChangePack serialized with
//     protobuf (proto) and then compressed with gzip (gzip). They are
//     computed once per Size/Gap subcase during setup, reported after the
//     timed loop, and therefore do not affect ns/op, B/op or allocs/op.
//     They do not depend on the Cache axis.
//
// What is not measured:
//
//   - The full PushPull path, RPC handling and network transfer. The gzip
//     size uses the default compression level and is only an estimate of the
//     transport size.
//   - Snapshot creation cost on the write path. The snapshot row is created
//     once in setup.
//   - Housekeeping and other background work of the backend.
//
// Matrix: Size (50KB, 500KB, 3MB) x Gap (50, 500, 1000) x Cache (Cold, Warm),
// which is 18 cases. Size is the approximate size of the generated text
// document. The number of changes is Size/40, so it grows with Size.
//
// Why the integration tag is required: the benchmark seeds documents and
// snapshots through a real backend, so it needs a running MongoDB (see
// helper.TestMongoConfig, localhost:27017 by default) and is excluded from
// the default test run. Run it with:
//
//	go test ./server/packs -tags integration -run '^$' \
//	  -bench BenchmarkSnapshotCacheMatrix_SizeGapCache \
//	  -benchmem -count=8 -benchtime=15x -timeout 0
//
// The 3MB cases dominate the runtime; the full matrix takes about 90 minutes
// on a laptop-class machine. Use -bench '.../Size=50KB' for a quick check.
// Absolute ns/op values vary between runs and machines, so compare cases
// within one run, or use benchstat across runs on the same machine.
package packs_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
	"github.com/yorkie-team/yorkie/server/profiling/prometheus"
	"github.com/yorkie-team/yorkie/test/helper"
)

// newMatrixBackend creates a fresh backend so that the snapshot cache of one
// subcase never leaks into another. The backend shares the MongoDB database
// with the package-level testBackend, so callers must use document and client
// keys that are unique per subcase.
func newMatrixBackend(b *testing.B) *backend.Backend {
	b.Helper()

	met, err := prometheus.NewMetrics()
	if err != nil {
		b.Fatalf("create metrics: %v", err)
	}

	be, err := backend.New(
		helper.TestBackendConfig(),
		helper.TestMongoConfig(),
		helper.TestMembershipConfig(),
		helper.TestHousekeepingConfig(),
		met, nil, nil,
	)
	if err != nil {
		b.Fatalf("create backend: %v", err)
	}
	b.Cleanup(func() {
		if err := be.Shutdown(); err != nil {
			b.Errorf("shutdown backend: %v", err)
		}
	})

	return be
}

// newMatrixClient activates a client directly on the DB of the given backend,
// bypassing the shared RPC server of the package, so that the client is
// scoped to the isolated backend.
func newMatrixClient(
	b *testing.B,
	ctx context.Context,
	be *backend.Backend,
	clientKey string,
) *database.ClientInfo {
	b.Helper()

	projectInfo, err := be.DB.FindProjectInfoByID(ctx, database.DefaultProjectID)
	if err != nil {
		b.Fatalf("find default project: %v", err)
	}

	clientInfo, err := be.DB.ActivateClient(
		ctx,
		projectInfo.ToProject().ID,
		clientKey,
		map[string]string{"userID": clientKey},
	)
	if err != nil {
		b.Fatalf("activate client: %v", err)
	}

	return clientInfo
}

// BenchmarkSnapshotCacheMatrix_SizeGapCache measures the cost of rebuilding a
// document with a cold or a warm snapshot cache, for several document sizes
// and gaps.
//
// Here the gap is the number of changes between the stored snapshot and the
// target server sequence, that is, the number of changes that must be
// replayed after the snapshot is loaded. Each Size/Gap subcase runs on its
// own backend.
func BenchmarkSnapshotCacheMatrix_SizeGapCache(b *testing.B) {
	sizeCases := []struct {
		name  string
		bytes int
	}{
		{"50KB", 50 * 1024},
		{"500KB", 500 * 1024},
		{"3MB", 3 * 1024 * 1024},
	}
	gapCases := []int64{50, 500, 1000}

	for _, sz := range sizeCases {
		for _, gap := range gapCases {

			totalChanges := max(sz.bytes/40, 1)
			if int64(totalChanges) < gap {
				totalChanges = int(gap)
			}

			b.Run(fmt.Sprintf("Size=%s/Gap=%d", sz.name, gap), func(b *testing.B) {
				ctx := context.Background()
				be := newMatrixBackend(b)

				clientKey := fmt.Sprintf("matrix-%s-gap-%d-client", sz.name, gap)
				clientInfo := newMatrixClient(b, ctx, be, clientKey)
				docKey := key.Key(fmt.Sprintf("matrix-%s-gap-%d-doc", sz.name, gap))

				// Only the server sequence matters here, so the client
				// checkpoint is left at the latest position (targetGap=0).
				// The seed 42 fixes the generated workload and thus the wire sizes.
				docInfo, _, _ := seedDocumentAtGap(
					b, ctx, be, docKey, clientInfo, totalChanges, 0, 42,
				)

				targetServerSeq := docInfo.ServerSeq
				snapshotServerSeq := max(targetServerSeq-gap, 0)

				// Build the document at snapshotServerSeq and store it as a
				// snapshot. This is setup cost and is not timed.
				snapshotDoc, err := packs.BuildInternalDocForServerSeq(
					ctx, be, docInfo, snapshotServerSeq,
				)
				if err != nil {
					b.Fatalf("build document at snapshot seq: %v", err)
				}
				if got := snapshotDoc.Checkpoint().ServerSeq; got != snapshotServerSeq {
					b.Fatalf(
						"snapshot checkpoint mismatch: got=%d want=%d",
						got, snapshotServerSeq,
					)
				}
				if err := be.DB.CreateSnapshotInfo(
					ctx, docInfo.RefKey(), snapshotDoc,
				); err != nil {
					b.Fatalf("create snapshot info: %v", err)
				}

				// Verify against the DB that exactly `gap` changes lie
				// between the snapshot and the target. These are the changes
				// replayed in every iteration.
				gapChanges, err := be.DB.FindChangesBetweenServerSeqs(
					ctx, docInfo.RefKey(), snapshotServerSeq+1, targetServerSeq,
				)
				if err != nil {
					b.Fatalf("find changes in gap: %v", err)
				}
				if int64(len(gapChanges)) != gap {
					b.Fatalf(
						"gap changes mismatch: got=%d want=%d",
						len(gapChanges), gap,
					)
				}

				// Rebuild once up front to confirm that the full replay
				// reaches the target. This also fills the cache, so purge it.
				targetDoc, err := packs.BuildInternalDocForServerSeq(
					ctx, be, docInfo, targetServerSeq,
				)
				if err != nil {
					b.Fatalf("build document at target seq: %v", err)
				}
				if got := targetDoc.Checkpoint().ServerSeq; got != targetServerSeq {
					b.Fatalf(
						"target checkpoint mismatch: got=%d want=%d",
						got, targetServerSeq,
					)
				}
				be.Cache.Snapshot.Purge()

				// Measure the wire size of both response forms once. This is
				// done outside the timed loops.
				gapInfos, err := be.DB.FindChangeInfosBetweenServerSeqs(
					ctx, docInfo.RefKey(), snapshotServerSeq+1, targetServerSeq,
				)
				if err != nil {
					b.Fatalf("find change infos in gap: %v", err)
				}
				if int64(len(gapInfos)) != gap {
					b.Fatalf(
						"gap change infos mismatch: got=%d want=%d",
						len(gapInfos), gap,
					)
				}
				chgPack := packs.NewServerPack(
					docInfo.Key,
					change.Checkpoint{ServerSeq: targetServerSeq},
					gapInfos,
					nil,
				)
				chgWire, err := measureWire(chgPack)
				if err != nil {
					b.Fatalf("measure changes pack: %v", err)
				}

				snapBytes, err := converter.SnapshotToBytes(
					targetDoc.RootObject(), targetDoc.AllPresences(),
				)
				if err != nil {
					b.Fatalf("serialize snapshot: %v", err)
				}
				snapPack := packs.NewServerPack(
					docInfo.Key,
					change.Checkpoint{ServerSeq: targetServerSeq},
					nil,
					snapBytes,
				)
				snapPack.VersionVector = targetDoc.VersionVector()
				snapWire, err := measureWire(snapPack)
				if err != nil {
					b.Fatalf("measure snapshot pack: %v", err)
				}
				b.Logf(
					"wire: changes(proto=%d gzip=%d) snapshot(proto=%d gzip=%d)",
					chgWire.Proto, chgWire.Gzip, snapWire.Proto, snapWire.Gzip,
				)

				b.Run("Cache=Cold", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						b.StopTimer()
						be.Cache.Snapshot.Purge()
						b.StartTimer()

						if _, err := packs.BuildInternalDocForServerSeq(
							ctx, be, docInfo, targetServerSeq,
						); err != nil {
							b.Fatalf("build with cold cache: %v", err)
						}
					}
					reportWire(b, "chg-", chgWire)
					reportWire(b, "snap-", snapWire)
				})

				b.Run("Cache=Warm", func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						// BuildInternalDocForServerSeq adds the rebuilt
						// document to the cache, so prime the cache with the
						// snapshot-time document on every iteration.
						// Otherwise later iterations would find the target
						// already cached and replay nothing.
						b.StopTimer()
						be.Cache.Snapshot.Purge()
						be.Cache.Snapshot.Add(docInfo.RefKey(), snapshotDoc)
						b.StartTimer()

						if _, err := packs.BuildInternalDocForServerSeq(
							ctx, be, docInfo, targetServerSeq,
						); err != nil {
							b.Fatalf("build with warm cache: %v", err)
						}
					}
					reportWire(b, "chg-", chgWire)
					reportWire(b, "snap-", snapWire)
				})
			})
		}
	}
}
