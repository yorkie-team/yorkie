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
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// newTreeReplayReplica returns a fresh document holding exactly what seed
// holds, built by replaying seed's own change pack through a REAL
// protobuf encode/decode -- not by handing seed's pack to ApplyChangePack
// directly. Two peers built this way share the same underlying node
// identities (the tickets came off the wire, not out of seed's memory),
// which is what lets a position minted on one resolve correctly on the
// other, exactly like two independent clients of the same document would.
func newTreeReplayReplica(
	t *testing.T,
	seed *document.Document,
	hexActor string,
	opts ...document.Option,
) *document.Document {
	t.Helper()

	pbPack, err := converter.ToChangePack(seed.CreateChangePack())
	assert.NoError(t, err)
	pack, err := converter.FromChangePack(pbPack)
	assert.NoError(t, err)

	replica := document.New(seed.Key(), opts...)
	actor, err := time.ActorIDFromHex(hexActor)
	assert.NoError(t, err)
	replica.SetActor(actor)
	pack.VersionVector.Set(replica.ActorID(), replica.VersionVector().VersionOf(replica.ActorID()))
	assert.NoError(t, replica.ApplyChangePack(pack))

	return replica
}

// TestTreeEditReplayGateDoesNotBreakClientReconciliation is the regression
// test the OpSourceReplay/OpSourceRemote split has no guard for: gating the
// TreeEdit bookkeeping applyChanges reads (NormalizePos, GetContentSize) on
// NeedsReverse() -- which collapses OpSourceRemote and OpSourceReplay into
// the same "skip" bucket -- would leave go test ./pkg/document/... fully
// green while silently corrupting a stacked undo entry on every remote tree
// edit a CLIENT receives. See OpSourceReplay's doc comment in
// pkg/document/operations/operation.go.
//
// Every other replica test in this package hands the SAME operation
// pointers to both documents in-process (CreateChangePack's result applied
// straight to ApplyChangePack), so a stale lastFromIdx left over from the
// sender's own OpSourceLocal execution survives untouched and masks a gate
// that wrongly skips setting it on the receiver. This test instead decodes
// the remote change through converter.ToChangePack/FromChangePack, so the
// TreeEdit the receiver executes is a value that has NEVER been executed
// anywhere before -- its lastFromIdx/lastToIdx start nil, and a wrong gate
// has nothing stale to hide behind: NormalizePos degrades straight to the
// (0, 0) fallback documented in TreeEdit.Execute.
func TestTreeEditReplayGateDoesNotBreakClientReconciliation(t *testing.T) {
	// Seed content both peers replay from the wire: <r><p>abcd</p></r>.
	seed := document.New("doc")
	assert.NoError(t, seed.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetNewTree("t", json.TreeNode{
			Type: "r",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: textNodeType, Value: "abcd"}},
			}},
		})
		return nil
	}))

	// d1 stays unsplit and later authors the remote edit. d2 is the
	// receiver under test; GC is disabled so a version vector that is not a
	// faithful two-client history (this test never syncs d2's split back to
	// d1) cannot fail a GC pass that has nothing to do with what is under
	// test here.
	d1 := newTreeReplayReplica(t, seed, "000000000000000000000001")
	d2 := newTreeReplayReplica(t, seed, "000000000000000000000002", document.WithDisableGC())

	// d2's own local split stacks a boundary-deletion reverse over the two
	// tokens the split introduced: a splitLevel 0 TreeEdit undo entry whose
	// range is [3, 5).
	splitTree(t, d2, 3, 1)
	assert.Equal(t, "<r><p>ab</p><p>cd</p></r>", treeXML(t, d2))

	top := d2.UndoStackTopForTest()
	assert.Len(t, top, 1, "the split's own boundary-deletion reverse")
	stacked, ok := top[0].Op.(*operations.TreeEdit)
	assert.True(t, ok)
	stackedFrom, stackedTo := stacked.NormalizePos()
	assert.Equal(t, 3, stackedFrom)
	assert.Equal(t, 5, stackedTo)

	// d1, still unsplit, inserts "XY" at the very back of the paragraph's
	// content -- entirely to the right of where d2's split landed.
	editTree(t, d1, 5, 5, textNode("XY"))

	pbPack, err := converter.ToChangePack(d1.CreateChangePack())
	assert.NoError(t, err)
	pack, err := converter.FromChangePack(pbPack)
	assert.NoError(t, err)
	assert.Len(t, pack.Changes, 1)
	remoteOp, ok := pack.Changes[0].Operations()[0].(*operations.TreeEdit)
	assert.True(t, ok, "the wire-decoded operation under test")

	pack.VersionVector.Set(d2.ActorID(), d2.VersionVector().VersionOf(d2.ActorID()))
	assert.NoError(t, d2.ApplyChangePack(pack))
	assert.Equal(t, "<r><p>ab</p><p>cdXY</p></r>", treeXML(t, d2))

	// Half 1: the decoded remote TreeEdit reports the RECEIVER's own
	// pre-edit index -- 7, the back of "cd" in d2's already-split tree --
	// not a value carried over from anywhere else.
	remoteFrom, remoteTo := remoteOp.NormalizePos()
	assert.Equal(t, 7, remoteFrom, "receiver's own pre-edit index")
	assert.Equal(t, 7, remoteTo)
	assert.Equal(t, 2, remoteOp.GetContentSize())

	// Half 2: the stacked split-undo entry is untouched. The remote insert
	// sits entirely to the right of [3, 5) (Case 2, a no-op), so the entry
	// must still read exactly as it did before the remote change arrived.
	stackedFrom, stackedTo = stacked.NormalizePos()
	assert.Equal(t, 3, stackedFrom, "must not have shifted")
	assert.Equal(t, 5, stackedTo)
}
