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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestDeepCopyCarriesAdoptedSlots pins the half of AdoptRefusedCopy's
// guarantee that a tree walk cannot reproduce. An orphan hangs off no
// container, so NewRoot -- which is all DeepCopy used to do -- cannot find it,
// and the copy stopped answering for every createdAt only the orphan carried.
// Document.applyChanges executes every remote change against such a copy
// before the root sees it, so the operation the adoption exists to keep
// resolvable failed there.
func TestDeepCopyCarriesAdoptedSlots(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	require.NoError(t, err)

	root := NewRoot(NewObject(NewElementRHT(), time.InitialTicket))

	orphanAt := time.NewTicket(1, 0, actor)
	onlyHereAt := time.NewTicket(2, 0, actor)
	orphan := NewObject(NewElementRHT(), orphanAt)
	child, err := NewPrimitive("v", onlyHereAt)
	require.NoError(t, err)
	orphan.Set("k", child)

	before := root.DocSize()
	root.AdoptRefusedCopy(orphan)
	require.NotNil(t, root.FindByCreatedAt(onlyHereAt))
	require.Equal(t, before, root.DocSize(), "an adopted copy is charged nothing")

	copied, err := root.DeepCopy()
	require.NoError(t, err)

	assert.NotNil(t, copied.FindByCreatedAt(orphanAt),
		"the clone stopped answering for the refused copy")
	assert.NotNil(t, copied.FindByCreatedAt(onlyHereAt),
		"the clone stopped answering for a descendant only the refused copy carries")
	assert.Equal(t, root.DocSize(), copied.DocSize(),
		"carrying the slots over must not charge the clone for them")
}

// TestDeepCopyDropsOrphansWhoseSlotsWereTakenOver keeps the carried-over list
// from growing with every restore: an orphan whose every slot has since been
// taken over by a live element is unreachable in the original too, so there is
// nothing for the copy to answer for.
func TestDeepCopyDropsOrphansWhoseSlotsWereTakenOver(t *testing.T) {
	actor, err := time.ActorIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaa")
	require.NoError(t, err)

	obj := NewObject(NewElementRHT(), time.InitialTicket)
	root := NewRoot(obj)

	sharedAt := time.NewTicket(1, 0, actor)
	orphan, err := NewPrimitive("old", sharedAt)
	require.NoError(t, err)
	root.AdoptRefusedCopy(orphan)
	require.Len(t, root.detached, 1)

	// A live element restored under the same createdAt takes the slot over.
	live, err := NewPrimitive("new", sharedAt)
	require.NoError(t, err)
	obj.Set("k", live)
	root.RegisterElement(live, obj)
	require.Same(t, Element(live), root.FindByCreatedAt(sharedAt))

	_, err = root.DeepCopy()
	require.NoError(t, err)
	assert.Empty(t, root.detached,
		"an orphan that answers for nothing must not be carried forever")
}
