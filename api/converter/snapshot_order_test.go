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

package converter_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// docWithRestoredKey builds a document whose root key holds a live member
// restored by undo/redo -- its `movedAt` is newer than its `createdAt` --
// alongside the tombstones the superseded writes left behind.
//
// That shape is what makes snapshot member order observable: while every key
// holds exactly one node, any arrival order rebuilds the same object.
func docWithRestoredKey(t *testing.T, redo bool) *document.Document {
	t.Helper()

	doc := document.New("d1")
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("frame", "v1")
		return nil
	}))
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("frame", "v2")
		return nil
	}))
	assert.NoError(t, doc.Undo())
	assert.Equal(t, `{"frame":"v1"}`, doc.Marshal())

	if redo {
		assert.NoError(t, doc.Redo())
		assert.Equal(t, `{"frame":"v2"}`, doc.Marshal())
	}

	return doc
}

// permute yields every ordering of the given slice.
func permute[T any](items []T) [][]T {
	if len(items) <= 1 {
		return [][]T{append([]T(nil), items...)}
	}

	var out [][]T
	for i := range items {
		rest := make([]T, 0, len(items)-1)
		rest = append(rest, items[:i]...)
		rest = append(rest, items[i+1:]...)
		for _, tail := range permute(rest) {
			out = append(out, append([]T{items[i]}, tail...))
		}
	}
	return out
}

// TestSnapshotDecodeIsOrderIndependent is the Go baseline: however the
// members of an encoded object are ordered on the wire, decoding must
// rebuild the same object.
//
// `fromJSONObject` replays every member through
// `ElementRHT.SetWithExecutedAt(key, elem, PositionedAt(elem))` in wire
// order, so this is a direct test of that method's order independence. It is
// the behavior the JS SDK's `ElementRHT.set` must match: JS gates eviction on
// the occupant's raw createdAt but the winner on its positionedAt, so a
// tombstone arriving after the live member evicts the member that then wins
// anyway, and the key reads as absent.
func TestSnapshotDecodeIsOrderIndependent(t *testing.T) {
	for _, tc := range []struct {
		name string
		redo bool
	}{
		{"key restored by undo", false},
		{"key restored by undo then redo", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := docWithRestoredKey(t, tc.redo)
			want := doc.Marshal()

			encoded, err := converter.ObjectToBytes(doc.RootObject())
			assert.NoError(t, err)

			pbElem := &api.JSONElement{}
			assert.NoError(t, proto.Unmarshal(encoded, pbElem))
			nodes := pbElem.GetJsonObject().GetNodes()
			assert.GreaterOrEqual(t, len(nodes), 2,
				"the key must carry a tombstone alongside the live member")
			// permute is factorial and each permutation costs a full
			// marshal + decode. Today the fixtures yield 2 members; fail
			// here rather than let a wider fixture quietly make this the
			// slowest test in the package.
			assert.LessOrEqual(t, len(nodes), 4, "permutation cost is factorial")

			for i, order := range permute(nodes) {
				// Rebuild the wrapper around the reordered members rather
				// than cloning: marshal only reads them, and a clone whose
				// nodes are then replaced by the originals buys nothing.
				pbObj := pbElem.GetJsonObject()
				shuffled := &api.JSONElement{
					Body: &api.JSONElement_JsonObject{
						JsonObject: &api.JSONElement_JSONObject{
							Nodes:     order,
							CreatedAt: pbObj.GetCreatedAt(),
							MovedAt:   pbObj.GetMovedAt(),
							RemovedAt: pbObj.GetRemovedAt(),
						},
					},
				}

				marshaled, err := proto.Marshal(shuffled)
				assert.NoError(t, err)

				obj, err := converter.BytesToObject(marshaled)
				assert.NoError(t, err)
				assert.Equal(t, want, obj.Marshal(),
					"permutation #%d rebuilt a different object", i)

				// Stronger than comparing the visible members: re-encoding
				// has to land back on the canonical bytes, so no permutation
				// may leave the tombstones carrying different timestamps
				// than the ones it started from.
				reencoded, err := converter.ObjectToBytes(obj)
				assert.NoError(t, err)
				assert.True(t, bytes.Equal(encoded, reencoded),
					"permutation #%d did not round-trip to the same bytes", i)
			}
		})
	}
}

// TestSnapshotEncodingIsDeterministic asserts that encoding the same object
// twice produces the same bytes.
//
// `toJSONObject` emits an object's members from `Object.RHTNodes()`, which is
// `ElementRHT.Nodes()` -- a `range` over the `nodeMapByCreatedAt` Go map. Map
// iteration order is randomized, so the member order in the emitted snapshot
// differs between calls. Every attach re-encodes, so two clients attaching to
// the SAME stored document receive the same members in different orders.
//
// On its own that is harmless: `TestSnapshotDecodeIsOrderIndependent` above
// is what makes it so. It stops being harmless for any peer whose decoder is
// order-sensitive -- which the JS SDK's is -- because the same unchanged
// document then resolves a key as present on one attach and absent on the
// next. Deterministic output also makes a snapshot byte-reproducible, which
// is what lets it be compared or cached at all.
func TestSnapshotEncodingIsDeterministic(t *testing.T) {
	for _, tc := range []struct {
		name  string
		build func(t *testing.T) *document.Document
	}{{
		name:  "object whose key was restored by undo",
		build: func(t *testing.T) *document.Document { return docWithRestoredKey(t, false) },
	}, {
		name: "plain object with several members",
		build: func(t *testing.T) *document.Document {
			doc := document.New("d2")
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetString("a", "1")
				root.SetString("b", "2")
				root.SetString("c", "3")
				root.SetString("d", "4")
				return nil
			}))
			return doc
		},
	}, {
		name: "nested object whose member was restored by undo",
		build: func(t *testing.T) *document.Document {
			// toJSONObject recurses, so a nested container's members are
			// emitted by the same path. Without a case here, only the root's
			// ordering would be covered.
			doc := document.New("d4")
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("el").SetString("frame", "v1")
				return nil
			}))
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.GetObject("el").SetString("frame", "v2")
				return nil
			}))
			assert.NoError(t, doc.Undo())
			assert.Equal(t, `{"el":{"frame":"v1"}}`, doc.Marshal())
			return doc
		},
	}, {
		name: "text with attributes",
		build: func(t *testing.T) *document.Document {
			doc := document.New("d3")
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewText("k").
					Edit(0, 0, "hello").
					Style(0, 5, map[string]string{"b": "1", "i": "1", "u": "1", "s": "1"})
				return nil
			}))
			return doc
		},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			doc := tc.build(t)

			first, err := converter.ObjectToBytes(doc.RootObject())
			assert.NoError(t, err)

			for i := 1; i < 100; i++ {
				again, err := converter.ObjectToBytes(doc.RootObject())
				assert.NoError(t, err)
				if !bytes.Equal(first, again) {
					t.Fatal(fmt.Sprintf("encode #%d differs from #0: member order is not stable", i))
				}
			}
		})
	}
}
