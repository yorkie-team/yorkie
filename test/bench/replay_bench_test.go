//go:build bench

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

package bench

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// BenchmarkChangeReplay measures the server's rebuild path: replaying stored
// changes into a fresh InternalDocument, as BuildInternalDocForServerSeq does
// for every snapshot, compaction and push-pull that misses the cache.
//
// Undo/redo made every operation build a reverse operation, which for Text
// means normalizing the edit's anchor over the whole physical `prev` chain --
// linear work per change, quadratic over the replay. The remote path discards
// the reverse (InternalDocument.ApplyChanges drops it), so it must not pay for
// it. This benchmark exists to keep that regression from returning silently:
// the numbers must stay close to linear in the edit count.
func BenchmarkChangeReplay(b *testing.B) {
	for _, cnt := range []int{400, 1600} {
		b.Run(fmt.Sprintf("text-%d-edits", cnt), func(b *testing.B) {
			changes := textEditChanges(b, cnt)
			b.ResetTimer()

			for range b.N {
				doc := document.NewInternalDocument("d1")
				if _, _, err := doc.ApplyChanges(changes...); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// textEditChanges builds cnt appending Text edits as a client would and
// returns the resulting changes, ready to be replayed as remote changes.
// The Text edit path leaves its operations untouched under OpSourceRemote,
// so the same slice can be replayed repeatedly.
func textEditChanges(b *testing.B, cnt int) []*change.Change {
	doc := document.New("d1")
	assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewText("text")
		return nil
	}))

	for i := range cnt {
		assert.NoError(b, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("text").Edit(i, i, "a")
			return nil
		}))
	}

	return doc.CreateChangePack().Changes
}
