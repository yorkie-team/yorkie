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

//go:build bench

package bench

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// roTypedDoc returns a document whose Text holds n characters typed one per
// Edit, front to back, so it has one node per character like a real typed
// document.
func roTypedDoc(b *testing.B, n int) *document.Document {
	b.Helper()
	doc := document.New("ro")
	require.NoError(b, doc.Update(func(root *json.Object, _ *presence.Presence) error {
		t := root.SetNewText("t")
		for i := range n {
			t.Edit(i, i, "x")
		}
		return nil
	}))
	return doc
}

func roInsert(b *testing.B, doc *document.Document, at int) {
	require.NoError(b, doc.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(at, at, "y")
		return nil
	}))
}

// BenchmarkRightOriginLocal measures one local single-character insert on a
// document of the given size.
func BenchmarkRightOriginLocal(b *testing.B) {
	for _, n := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("sequential/%d", n), func(b *testing.B) {
			doc := roTypedDoc(b, n)
			at := n / 2
			for b.Loop() {
				roInsert(b, doc, at)
				at++
			}
		})
		b.Run(fmt.Sprintf("random/%d", n), func(b *testing.B) {
			doc := roTypedDoc(b, n)
			rnd := rand.New(rand.NewSource(1))
			size := n
			for b.Loop() {
				roInsert(b, doc, rnd.Intn(size+1))
				size++
			}
		})
	}
}

// BenchmarkRightOriginRemote measures applying a pack of 100 single-character
// inserts made by another replica: without concurrency (the remote typed
// into a synced document) and with it (both replicas typed 100 characters
// backward at the same index before syncing).
func BenchmarkRightOriginRemote(b *testing.B) {
	const base, run = 10_000, 100
	actor := func(i int) time.ActorID {
		a, err := time.ActorIDFromHex(fmt.Sprintf("%024x", i))
		require.NoError(b, err)
		return a
	}
	setup := func(concurrent bool) (*document.Document, *change.Pack) {
		d1 := document.New("ro")
		d1.SetActor(actor(1))
		require.NoError(b, d1.Update(func(root *json.Object, _ *presence.Presence) error {
			root.SetNewText("t").Edit(0, 0, strings.Repeat("x", base))
			return nil
		}))
		d2 := document.New("ro")
		d2.SetActor(actor(2))
		require.NoError(b, d2.ApplyChangePack(d1.CreateChangePack()))
		for range run {
			require.NoError(b, d2.Update(func(root *json.Object, _ *presence.Presence) error {
				root.GetText("t").Edit(base/2, base/2, "b")
				return nil
			}))
		}
		if concurrent {
			for range run {
				require.NoError(b, d1.Update(func(root *json.Object, _ *presence.Presence) error {
					root.GetText("t").Edit(base/2, base/2, "a")
					return nil
				}))
			}
		}
		p := d2.CreateChangePack()
		return d1, change.NewPack(p.DocumentKey, change.NewCheckpoint(0, 0), p.Changes, time.InitialVersionVector, nil)
	}
	for _, concurrent := range []bool{false, true} {
		b.Run(fmt.Sprintf("concurrent=%v", concurrent), func(b *testing.B) {
			for b.Loop() {
				b.StopTimer()
				d1, pack := setup(concurrent)
				b.StartTimer()
				require.NoError(b, d1.ApplyChangePack(pack))
			}
		})
	}
}

// BenchmarkRightOriginSize reports the encoded size of a typed document's
// snapshot and of the change pack that typed it, per character.
func BenchmarkRightOriginSize(b *testing.B) {
	const n = 10_000
	doc := document.New("ro")
	for i := range n {
		require.NoError(b, doc.Update(func(root *json.Object, _ *presence.Presence) error {
			if i == 0 {
				root.SetNewText("t")
			}
			root.GetText("t").Edit(i, i, "x")
			return nil
		}))
	}
	pbPack, err := converter.ToChangePack(doc.CreateChangePack())
	require.NoError(b, err)
	var opBytes int
	for _, c := range pbPack.Changes {
		for _, op := range c.Operations {
			if op.GetEdit() != nil {
				opBytes += proto.Size(op)
			}
		}
	}
	var snapshot []byte
	for b.Loop() {
		snapshot, err = converter.ObjectToBytes(doc.RootObject())
		require.NoError(b, err)
	}
	b.ReportMetric(float64(len(snapshot))/n, "snapshot-B/char")
	b.ReportMetric(float64(opBytes)/n, "edit-op-B/char")
}
