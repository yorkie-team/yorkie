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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// TestSnapshotDropsRemovedTreeAttrValue covers the decode boundary of the
// "a tombstone carries no value" invariant.
//
// RHT.Remove mints a tombstone with no value, so a tombstone's size is a
// function of its key alone. A snapshot written before that change -- and a
// payload from any peer still on the old code -- carries the replaced value on
// the removed attribute, and restoring it would charge docSize.GC for bytes the
// replica that performed the removal never charged. Total() gates
// MaxSizeLimit, so the two would disagree about the same document's quota.
//
// The planted value stands in for exactly that legacy payload: the current
// encoder cannot produce one, because the node it serializes no longer holds a
// value.
func TestSnapshotDropsRemovedTreeAttrValue(t *testing.T) {
	doc := document.New("d")
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetNewTree("t", json.TreeNode{
			Type: "doc",
			Children: []json.TreeNode{{
				Type:     "p",
				Children: []json.TreeNode{{Type: "text", Value: "hello"}},
			}},
		})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").Style(0, 7, map[string]string{"b": "a-long-attribute-value"})
		return nil
	}))
	require.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetTree("t").RemoveStyle(0, 7, []string{"b"})
		return nil
	}))

	encoded, err := converter.ObjectToBytes(doc.RootObject())
	require.NoError(t, err)

	pbElem := &api.JSONElement{}
	require.NoError(t, proto.Unmarshal(encoded, pbElem))

	planted := 0
	for _, pbNode := range pbElem.GetJsonObject().GetNodes() {
		for _, pbTreeNode := range pbNode.GetElement().GetTree().GetNodes() {
			for _, pbAttr := range pbTreeNode.GetAttributes() {
				if pbAttr.GetIsRemoved() {
					pbAttr.Value = "a-long-attribute-value"
					planted++
				}
			}
		}
	}
	require.NotZero(t, planted, "no attribute tombstone reached the snapshot")

	legacy, err := proto.Marshal(pbElem)
	require.NoError(t, err)
	obj, err := converter.BytesToObject(legacy)
	require.NoError(t, err)
	root := crdt.NewRoot(obj)

	assert.Equal(t, doc.DocSize().GC, root.DocSize().GC,
		"a tombstone restored from a legacy payload must weigh its key alone")
	assert.Equal(t, doc.DocSize().Live, root.DocSize().Live)
	assert.Equal(t, doc.GarbageLen(), root.GarbageLen())
}
