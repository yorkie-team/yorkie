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
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// An attribute's updatedAt is what RHTNode.IDString is built from, and NewRoot
// reads it to key the GC pair a removed attribute registers. An attribute that
// arrives without one therefore faults inside NewRoot -- not while decoding the
// bytes that carried it, but on every later load of the document those bytes
// were persisted into.
//
// No Yorkie encoder produces this shape: RHT.Remove always stamps the removal
// ticket. But client-supplied bytes reach BytesToObject through a Set or Add
// operation payload, so a poisoned element could be stored once and then break
// every subsequent load. Both decode paths refuse it instead.
//
// Honouring isRemoved on the text path is what brought that path into the same
// blast radius as the tree one, which is why both are covered here.
//
// The payloads are built by encoding a real document and clearing exactly one
// field, rather than by hand. A hand-built JSONElement is rejected earlier for
// unrelated reasons ("json object is nil"), which would make this pass without
// ever reaching the code under test.
func TestAttributeWithoutUpdatedAtIsRejected(t *testing.T) {
	t.Run("text node attribute", func(t *testing.T) {
		doc := document.New("d")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewText("k").Edit(0, 0, "abcdefghij")
			return nil
		}))
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetText("k").Style(0, 10, map[string]string{"b": "1"})
			return nil
		}))

		encoded, err := converter.ObjectToBytes(doc.RootObject())
		require.NoError(t, err)
		_, err = converter.BytesToObject(encoded)
		require.NoError(t, err, "the untouched document must decode cleanly")

		poisoned := poisonAttrTicket(t, encoded, func(el *api.JSONElement) int {
			text := el.GetText()
			if text == nil {
				return 0
			}
			cleared := 0
			for _, node := range text.Nodes {
				for _, attr := range node.Attributes {
					attr.UpdatedAt = nil
					cleared++
				}
			}
			return cleared
		})

		_, err = converter.BytesToObject(poisoned)
		require.Error(t, err, "a text attribute with no updatedAt must be refused")
	})

	t.Run("tree node attribute", func(t *testing.T) {
		doc := document.New("d")
		require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.SetNewTree("t", json.TreeNode{Type: "doc", Children: []json.TreeNode{
				{Type: "p", Children: []json.TreeNode{{Type: "text", Value: "abc"}}},
			}})
			root.GetTree("t").Style(0, 5, map[string]string{"bold": "true"})
			return nil
		}))

		encoded, err := converter.ObjectToBytes(doc.RootObject())
		require.NoError(t, err)
		_, err = converter.BytesToObject(encoded)
		require.NoError(t, err, "the untouched document must decode cleanly")

		poisoned := poisonAttrTicket(t, encoded, func(el *api.JSONElement) int {
			tree := el.GetTree()
			if tree == nil {
				return 0
			}
			cleared := 0
			for _, node := range tree.Nodes {
				for _, attr := range node.Attributes {
					attr.UpdatedAt = nil
					cleared++
				}
			}
			return cleared
		})

		_, err = converter.BytesToObject(poisoned)
		require.Error(t, err, "a tree attribute with no updatedAt must be refused")
	})
}

// poisonAttrTicket decodes the encoded root, walks every member applying clear,
// and re-encodes. It fails the test when clear found nothing to strip, so a
// change to the encoding cannot quietly turn these cases into no-ops that pass
// for the wrong reason.
func poisonAttrTicket(
	t *testing.T,
	encoded []byte,
	clear func(*api.JSONElement) int,
) []byte {
	t.Helper()

	pbObj := &api.JSONElement{}
	require.NoError(t, proto.Unmarshal(encoded, pbObj))

	cleared := 0
	for _, node := range pbObj.GetJsonObject().GetNodes() {
		cleared += clear(node.Element)
	}
	require.NotZero(t, cleared, "no attribute ticket was cleared; the fixture is stale")

	poisoned, err := proto.Marshal(pbObj)
	require.NoError(t, err)
	return poisoned
}
