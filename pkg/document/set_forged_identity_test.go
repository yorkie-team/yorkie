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
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestSetOnForgedIdentityCollision pins that a local Set stays a plain Set
// when the document it runs against has been shaped to make it lose.
//
// The shape takes two things a peer controls, because an operation's tickets
// are not validated off the wire (tracked at the push boundary in
// yorkie-team/yorkie#2081): a member planted under the createdAt this client
// will mint next -- predictable, since it is the next delimiter of the next
// change ID -- and an occupant of the target key positioned far in the
// future, which makes the fresh ticket lose the LWW comparison. Together
// they are the only way a locally created value reaches ElementRHT's loser
// branch at all.
//
// Reaching it must not be fatal. json.Object.setInternal has by then handed
// the caller a proxy for the value, so it has nothing to do with a refusal
// but panic -- and a panic here escapes Document.Update past
// invalidateClone, taking down a server that runs json proxies over a
// document rebuilt from stored client changes (server/revisions,
// server/documents). The refusal belongs to the restore path, which has a
// caller able to skip the operation; crdt.Object.Set does not refuse.
func TestSetOnForgedIdentityCollision(t *testing.T) {
	newRoot := func() *crdt.Root {
		return crdt.NewRoot(crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket))
	}

	// The context issues tickets off change.InitialID, so an identical one
	// answers with the ticket the Set below will mint.
	forged := change.NewContext(change.InitialID(), "", newRoot()).IssueTimeTicket()

	root := newRoot()
	ctx := change.NewContext(change.InitialID(), "", root)

	planted, err := crdt.NewPrimitive("planted", forged)
	require.NoError(t, err)
	root.Object().Set("planted", planted)
	root.RegisterElement(planted, root.Object())

	occupant, err := crdt.NewPrimitive("occupant", time.NewTicket(1<<40, 0, time.InitialActorID))
	require.NoError(t, err)
	root.Object().SetWithExecutedAt("k", occupant, crdt.PositionedAt(occupant))
	root.RegisterElement(occupant, root.Object())

	obj := json.NewObject(ctx, root.Object())
	require.NotPanics(t, func() { obj.SetString("k", "v") },
		"a local Set crashed on document state a peer can shape")

	// The planted member keeps the key it was planted under: losing the LWW
	// comparison is what the loser branch is for, and it leaves the key's
	// occupant alone.
	assert.Same(t, occupant, root.Object().Get("k"))
}
