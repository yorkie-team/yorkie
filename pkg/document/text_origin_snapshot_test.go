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
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

func originString(o *crdt.InsertOrigin) string {
	if o == nil {
		return "nil"
	}
	r := "end"
	if o.Right != nil {
		r = o.Right.ToTestString()
	}
	return o.Left.ToTestString() + "|" + r
}

// TestOriginSnapshotRoundTrip edits two replicas concurrently, encodes the
// result as a snapshot, decodes it, and checks every node's origin survives.
func TestOriginSnapshotRoundTrip(t *testing.T) {
	for seed := int64(1); seed <= 50; seed++ {
		rnd := rand.New(rand.NewSource(seed))
		d1, d2 := intentShared(t, "0123456789")
		for range 5 {
			for _, d := range []*document.Document{d1, d2} {
				for range 4 {
					n := len([]rune(intentText(d)))
					at := rnd.Intn(n + 1)
					to := at
					if rnd.Intn(4) == 0 && at < n {
						to = at + 1
					}
					intentInsertRange(t, d, at, to, fmt.Sprint(rnd.Intn(10)))
				}
			}
			crossSync(t, d1, d2)
		}
		require.Equal(t, intentText(d1), intentText(d2))

		bytes, err := converter.ObjectToBytes(d1.RootObject())
		require.NoError(t, err)
		obj, err := converter.BytesToObject(bytes)
		require.NoError(t, err)
		decoded := obj.Get("t").(*crdt.Text)
		orig := d1.Root().GetText("t").Nodes()
		got := decoded.Nodes()
		require.Len(t, got, len(orig))
		for i := range orig {
			require.Equal(t, originString(orig[i].Origin()), originString(got[i].Origin()), "seed %d node %d", seed, i)
		}
	}
}

func intentInsertRange(t *testing.T, d *document.Document, from, to int, s string) {
	t.Helper()
	require.NoError(t, d.Update(func(root *json.Object, _ *presence.Presence) error {
		root.GetText("t").Edit(from, to, s)
		return nil
	}))
}
