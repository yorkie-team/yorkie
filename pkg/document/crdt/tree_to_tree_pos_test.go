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

package crdt_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/test/helper"
)

// toTreePos walks up from a removed node until it finds one that is still
// alive, dereferencing Index.Parent on every hop. Purge unlinks a node by
// clearing its own Index.Parent and leaves it registered nowhere, so a chain
// that ends in a purged node runs the walk off the top of the tree.
//
// This drives the state directly rather than through an undo, on purpose. The
// restore path that used to produce it no longer can -- recreateFromSpan now
// refuses to place a node live under a tombstone -- so a test that reached the
// guard through Document.Undo would stop reaching it and start passing without
// exercising anything. The guard is defence in depth and outlives the one
// sequence that was known to need it, so it is pinned here at the level it
// lives at.
func TestToTreePosRejectsAChainEndingInAPurgedNode(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>

	p := tree.Root().Children()[0]
	text := p.Children()[0]

	// Remove the whole <p>, tombstoning it and its text.
	_, _, err := tree.EditT(0, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	require.Equal(t, "<r></r>", tree.ToXML())
	require.True(t, p.IsRemoved())
	require.True(t, text.IsRemoved())

	// Purge <p> while its text child is still around. Purge clears the purged
	// node's own parent link and touches none of its children, so the text
	// node keeps pointing at a <p> that no longer hangs off the root.
	require.NoError(t, tree.Purge(p))
	require.Nil(t, p.Index.Parent, "purge unlinks the node it purges")
	require.NotNil(t, text.Index.Parent, "but leaves its children pointing at it")

	// Resolving a position anchored at the text node now walks text -> <p> ->
	// nil. Before the guard this dereferenced nil and took the process down.
	_, err = tree.ToIndex(text, text)
	require.ErrorIs(t, err, crdt.ErrNodeNotFound)
}

// The same walk must stay untouched when the chain does reach an alive
// ancestor, which is every ordinary removed-node lookup.
func TestToTreePosStillResolvesThroughARemovedParent(t *testing.T) {
	ctx := helper.TextChangeContext(helper.TestRoot())
	tree := createHelloTree(t, ctx) // <r><p>hello</p></r>

	p := tree.Root().Children()[0]
	text := p.Children()[0]

	_, _, err := tree.EditT(0, 7, nil, 0, helper.TimeT(ctx), issueTicket(ctx))
	require.NoError(t, err)
	require.True(t, text.IsRemoved())

	// Nothing is purged, so the walk from the removed text node reaches the
	// live root and resolves normally.
	idx, err := tree.ToIndex(text, text)
	require.NoError(t, err)
	require.GreaterOrEqual(t, idx, 0)
}
