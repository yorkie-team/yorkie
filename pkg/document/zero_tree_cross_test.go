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

// PROTOTYPE -- cross-SDK check of the Zero Tree server half. The cases are a
// JSONL file the JS harness wrote (a small sample is checked in under
// testdata; ZT_CASES names a larger one): per script, the server log (protobuf
// Change bytes in server order) and the snapshot a JS replica made by
// replaying that log, without GC and with a fixed GC schedule. The server
// replays the same log the same way; the Zero Tree element of its snapshot
// must be byte-equal to the JS one.

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/hex"
	gojson "encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
)

type zeroCrossCase struct {
	Seed       int      `json:"seed"`
	Changes    []string `json:"changes"`
	GCEvery    int      `json:"gcEvery"`
	Snapshot   string   `json:"snapshot"`
	SnapshotGC string   `json:"snapshotGC"`
	// GCMarks are the min version vectors the harness server had at each
	// sync (what a server collects with), with the log length then.
	GCMarks       []zeroGCMark `json:"gcMarks"`
	SnapshotMarks string       `json:"snapshotMarks"`
	// The JS model (`ZeroTree.toJSON`) of each replay.
	JSON      string `json:"json"`
	JSONGC    string `json:"jsonGC"`
	JSONMarks string `json:"jsonMarks"`
}

// ysonRoundTrip does what a compaction checks and a revision restore does:
// root -> YSON -> a new document from that YSON -> YSON again. It returns
// whether the two YSONs match and the rebuilt tree's model.
func ysonRoundTrip(t *testing.T, snapshot []byte) (bool, string) {
	obj, _, err := converter.BytesToSnapshot(snapshot)
	require.NoError(t, err)
	root, err := yson.FromCRDT(obj)
	require.NoError(t, err)
	text, err := root.(yson.Object).Marshal()
	require.NoError(t, err)
	var parsed yson.Object
	require.NoError(t, yson.Unmarshal(text, &parsed))
	newDoc := document.New("rebuilt")
	require.NoError(t, newDoc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetYSON(parsed)
		return nil
	}))
	again, err := yson.FromCRDT(newDoc.RootObject())
	require.NoError(t, err)
	againText, err := again.(yson.Object).Marshal()
	require.NoError(t, err)
	zt, ok := newDoc.RootObject().Get("zt").(*crdt.ZeroTree)
	require.True(t, ok)
	return text == againText, zt.Marshal()
}

// zeroModel decodes a snapshot and returns the server's model of its "zt".
func zeroModel(t *testing.T, snapshot []byte) string {
	obj, _, err := converter.BytesToSnapshot(snapshot)
	require.NoError(t, err)
	zt, ok := obj.Get("zt").(*crdt.ZeroTree)
	require.True(t, ok)
	return zt.Marshal()
}

type zeroGCMark struct {
	At int    `json:"at"`
	VV string `json:"vv"`
}

// zeroTreeElement returns the Zero Tree element "zt" of a snapshot, marshaled.
func zeroTreeElement(t *testing.T, snapshot []byte) []byte {
	pbSnapshot := &api.Snapshot{}
	require.NoError(t, proto.Unmarshal(snapshot, pbSnapshot))
	for _, node := range pbSnapshot.Root.GetJsonObject().GetNodes() {
		if node.Key == "zt" {
			_, ok := node.Element.Body.(*api.JSONElement_ZeroTree_)
			require.True(t, ok, "zt is not a ZeroTree")
			b, err := proto.MarshalOptions{Deterministic: true}.Marshal(node.Element)
			require.NoError(t, err)
			return b
		}
	}
	t.Fatal("no zt in snapshot")
	return nil
}

func minNonZero(vectors []time.VersionVector) time.VersionVector {
	vv := time.MinVersionVector(vectors...)
	for k, v := range vv {
		if v == 0 {
			delete(vv, k)
		}
	}
	return vv
}

type replayStats struct {
	gcRuns, purged int
}

func replayZeroLog(
	t *testing.T,
	changes []*change.Change,
	gcEvery int,
	marks []zeroGCMark,
) ([]byte, replayStats, error) {
	var stats replayStats
	doc := document.NewInternalDocument("server-replica")
	actors := make(map[time.ActorID]bool)
	for _, c := range changes {
		actors[c.ID().ActorID()] = true
	}
	latest := make(map[time.ActorID]time.VersionVector)
	for i, c := range changes {
		if _, _, err := doc.ApplyChangesForReplay(c); err != nil {
			return nil, stats, fmt.Errorf("change %d: %w", i+1, err)
		}
		latest[c.ID().ActorID()] = c.ID().VersionVector()
		n := i + 1
		if gcEvery > 0 && n%gcEvery == 0 && len(latest) == len(actors) {
			vectors := make([]time.VersionVector, 0, len(latest))
			for _, v := range latest {
				vectors = append(vectors, v)
			}
			if vv := minNonZero(vectors); len(vv) > 0 {
				purged, err := doc.GarbageCollect(vv)
				require.NoError(t, err)
				stats.gcRuns++
				stats.purged += purged
			}
		}
		for _, m := range marks {
			if m.At != n {
				continue
			}
			raw, err := hex.DecodeString(m.VV)
			require.NoError(t, err)
			pbVV := &api.VersionVector{}
			require.NoError(t, proto.Unmarshal(raw, pbVV))
			vv, err := converter.FromVersionVector(pbVV)
			require.NoError(t, err)
			purged, err := doc.GarbageCollect(vv)
			require.NoError(t, err)
			stats.gcRuns++
			stats.purged += purged
		}
	}
	snapshot, err := converter.SnapshotToBytes(doc.RootObject(), nil)
	require.NoError(t, err)
	return snapshot, stats, nil
}

//nolint:gocyclo // one pass over every check, so a case reports all of them
func TestZeroTreeCrossSDK(t *testing.T) {
	// ZT_CASES picks a larger export; by default the checked-in sample runs.
	path := os.Getenv("ZT_CASES")
	if path == "" {
		path = "testdata/zero_tree_cross.jsonl.gz"
	}
	f, err := os.Open(path) //nolint:gosec // a test fixture path
	require.NoError(t, err)
	defer func() { _ = f.Close() }()

	var r io.Reader = f
	if strings.HasSuffix(path, ".gz") {
		gz, err := gzip.NewReader(f)
		require.NoError(t, err)
		defer func() { _ = gz.Close() }()
		r = gz
	}

	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<20), 64<<20)
	var cases, ops, reencodeDiff, plainDiff, gcDiff, marksDiff, snapRoundTripDiff int
	var stats, markStats replayStats
	var purgedEntries, purgedCancelled, bothFailed, modelCases, modelDiff, ysonDiff int
	var failed []string
	for scanner.Scan() {
		var c zeroCrossCase
		require.NoError(t, gojson.Unmarshal(scanner.Bytes(), &c))
		cases++

		var changes []*change.Change
		for _, h := range c.Changes {
			raw, err := hex.DecodeString(h)
			require.NoError(t, err)
			pbChange := &api.Change{}
			require.NoError(t, proto.Unmarshal(raw, pbChange))
			decoded, err := converter.FromChanges([]*api.Change{pbChange})
			require.NoError(t, err)
			ops += len(pbChange.Operations)

			// The server stores what it decoded; re-encoding must give the
			// client's bytes back.
			pbAgain, err := converter.ToChanges(decoded)
			require.NoError(t, err)
			again, err := proto.Marshal(pbAgain[0])
			require.NoError(t, err)
			// Map entries (the change's version vector) may come in any order,
			// so the messages are compared, not the bytes.
			pbRaw, pbAgainMsg := &api.Change{}, &api.Change{}
			require.NoError(t, proto.Unmarshal(raw, pbRaw))
			require.NoError(t, proto.Unmarshal(again, pbAgainMsg))
			if !proto.Equal(pbRaw, pbAgainMsg) {
				reencodeDiff++
			}
			changes = append(changes, decoded...)
		}

		jsPlain, _ := hex.DecodeString(c.Snapshot)
		jsGC, _ := hex.DecodeString(c.SnapshotGC)

		goPlain, _, err := replayZeroLog(t, changes, 0, nil)
		require.NoError(t, err)
		if same, model := ysonRoundTrip(t, goPlain); !same || model != c.JSON {
			ysonDiff++
			if os.Getenv("ZT_DEBUG") != "" && ysonDiff == 1 {
				t.Logf("yson diff seed %d same %v\n rebuilt %s\n js      %s", c.Seed, same, model, c.JSON)
			}
		}
		if c.JSON != "" {
			modelCases++
			if m := zeroModel(t, goPlain); m != c.JSON {
				modelDiff++
				if os.Getenv("ZT_DEBUG") != "" && modelDiff == 1 {
					t.Logf("model diff seed %d\n go %s\n js %s", c.Seed, m, c.JSON)
				}
			}
		}
		if !bytes.Equal(zeroTreeElement(t, goPlain), zeroTreeElement(t, jsPlain)) {
			plainDiff++
			failed = append(failed, fmt.Sprintf("%d:plain", c.Seed))
		}
		// Changes are re-decoded for each replay so the replays are independent.
		redecode := func() []*change.Change {
			var out []*change.Change
			for _, h := range c.Changes {
				raw, _ := hex.DecodeString(h)
				pbChange := &api.Change{}
				require.NoError(t, proto.Unmarshal(raw, pbChange))
				decoded, err := converter.FromChanges([]*api.Change{pbChange})
				require.NoError(t, err)
				out = append(out, decoded...)
			}
			return out
		}
		goMarks, ms, goErr := replayZeroLog(t, redecode(), 0, c.GCMarks)
		markStats.gcRuns += ms.gcRuns
		markStats.purged += ms.purged
		if c.SnapshotMarks == "" || goErr != nil {
			// The JS replay failed on this log (GC then undo, see the report):
			// the server has to fail on the same change.
			if c.SnapshotMarks == "" && goErr != nil {
				bothFailed++
				t.Logf("seed %d: JS and Go both fail: %v", c.Seed, goErr)
			} else {
				marksDiff++
				failed = append(failed, fmt.Sprintf("%d:marks-error-mismatch(go=%v)", c.Seed, goErr))
			}
			continue
		}
		// Purged ledgers in the server's snapshot, and those a later undo
		// emptied: the restore-after-purge path ran.
		pbZT := &api.JSONElement{}
		require.NoError(t, proto.Unmarshal(zeroTreeElement(t, goMarks), pbZT))
		for _, e := range pbZT.GetZeroTree().PurgedLedger {
			purgedEntries++
			if len(e.Dels) == 0 {
				purgedCancelled++
			}
		}
		if c.JSONMarks != "" && zeroModel(t, goMarks) != c.JSONMarks {
			modelDiff++
		}
		jsMarks, _ := hex.DecodeString(c.SnapshotMarks)
		if !bytes.Equal(zeroTreeElement(t, goMarks), zeroTreeElement(t, jsMarks)) {
			marksDiff++
			failed = append(failed, fmt.Sprintf("%d:marks", c.Seed))
		}
		goGC, s, err := replayZeroLog(t, redecode(), c.GCEvery, nil)
		require.NoError(t, err)
		if c.JSONGC != "" && zeroModel(t, goGC) != c.JSONGC {
			modelDiff++
		}
		stats.gcRuns += s.gcRuns
		stats.purged += s.purged
		if !bytes.Equal(zeroTreeElement(t, goGC), zeroTreeElement(t, jsGC)) {
			if os.Getenv("ZT_DEBUG") != "" && gcDiff == 0 {
				a, b := &api.JSONElement{}, &api.JSONElement{}
				_ = proto.Unmarshal(zeroTreeElement(t, jsGC), a)
				_ = proto.Unmarshal(zeroTreeElement(t, goGC), b)
				t.Logf("seed %d JS element:\n%s\nGo element:\n%s", c.Seed, protojson.Format(a), protojson.Format(b))
			}
			gcDiff++
			failed = append(failed, fmt.Sprintf("%d:gc", c.Seed))
		}

		// A snapshot the JS SDK made decodes and re-encodes unchanged here.
		obj, _, err := converter.BytesToSnapshot(jsGC)
		require.NoError(t, err)
		reSnap, err := converter.SnapshotToBytes(obj, nil)
		require.NoError(t, err)
		if !bytes.Equal(zeroTreeElement(t, reSnap), zeroTreeElement(t, jsGC)) {
			snapRoundTripDiff++
		}
	}
	require.NoError(t, scanner.Err())

	if len(failed) > 10 {
		failed = failed[:10]
	}
	t.Logf("cases %d, ops %d, re-encode diff %d, plain diff %d, gc diff %d (gc runs %d, purged %d), "+
		"server-vv gc diff %d (gc runs %d, purged %d, purged ledgers %d, emptied by undo %d), "+
		"JS snapshot round trip diff %d, JS+Go both failed %d, "+
		"model (Go Marshal vs JS toJSON) cases %d diff %d, YSON round trip diff %d, first failures %v",
		cases, ops, reencodeDiff, plainDiff, gcDiff, stats.gcRuns, stats.purged,
		marksDiff, markStats.gcRuns, markStats.purged, purgedEntries, purgedCancelled,
		snapRoundTripDiff, bothFailed, modelCases, modelDiff, ysonDiff, failed)
	require.Zero(t, reencodeDiff+plainDiff+gcDiff+marksDiff+snapRoundTripDiff+modelDiff+ysonDiff)
}
