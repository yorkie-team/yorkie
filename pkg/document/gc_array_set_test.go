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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestArraySetCollectsTheReplacedElement pins that assigning into an array
// releases the element it displaced.
//
// `ArraySet.Execute` inserts the new value, deletes the old one and then
// discards what the delete handed back, so the displaced element was never
// registered for collection: it stayed charged to `docSize.Live` and no
// collection could reach it. No undo is involved -- an ordinary `arr[i] = x`
// in a loop grew the document without bound, and `docSize` is what the server
// enforces against its size limit.
func TestArraySetCollectsTheReplacedElement(t *testing.T) {
	doc := document.New("array-set-gc")
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddInteger(0)
		return nil
	}))

	set := func(v int) {
		require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetArray("arr").SetInteger(0, v)
			return nil
		}))
		doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
	}

	// One cycle establishes the steady state: an array holding one integer,
	// with the element it displaced collected.
	set(1)
	steady := doc.DocSize()
	require.Equal(t, 0, doc.GarbageLen())

	for i := 2; i <= 10; i++ {
		set(i)
	}

	assert.Equal(t, `{"arr":[10]}`, doc.Marshal())
	assert.Equal(t, 0, doc.GarbageLen())
	assert.Equal(t, steady, doc.DocSize(),
		fmt.Sprintf("nine more assignments grew the document: %v -> %v",
			steady, doc.DocSize()))
}

// TestArraySetDoesNotExhaustTheSizeLimit pins that repeated assignment does
// not walk a document into its size limit.
//
// `Update` checks MaxSizeLimit against the clone root it builds
// (document.go:257-258), not against the root the operation is replayed on,
// so fixing only `ArraySet.Execute` left the limit gated on an accounting
// that still grew without bound: at a limit of 300 over a baseline of 100,
// the eighth `arr[0] = v` was refused while `DocSize()` still reported 100.
func TestArraySetDoesNotExhaustTheSizeLimit(t *testing.T) {
	doc := document.New("array-set-size-limit")
	doc.MaxSizeLimit = 300
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddInteger(0)
		return nil
	}))

	for i := 1; i <= 30; i++ {
		require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
			r.GetArray("arr").SetInteger(0, i)
			return nil
		}), "assignment %d was refused", i)
		doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
	}

	assert.Equal(t, `{"arr":[30]}`, doc.Marshal())
	size := doc.DocSize()
	assert.Equal(t, 100, size.Total())
}

// TestArraySetKeepsTheReplacedElementAddressable pins the other half: the
// element the assignment displaced is a tombstone, not a hole. Collecting it
// must not disturb the value that replaced it.
func TestArraySetKeepsTheReplacedElementAddressable(t *testing.T) {
	doc := document.New("array-set-addressable")
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewArray("arr").AddInteger(1).AddInteger(2).AddInteger(3)
		return nil
	}))
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetInteger(1, 99)
		return nil
	}))
	doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))

	assert.Equal(t, `{"arr":[1,99,3]}`, doc.Marshal())

	// The replacement is still a normal member: it can be read, replaced
	// again and removed.
	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").SetInteger(1, 100)
		return nil
	}))
	assert.Equal(t, `{"arr":[1,100,3]}`, doc.Marshal())

	require.NoError(t, doc.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetArray("arr").Delete(1)
		return nil
	}))
	doc.GarbageCollect(helper.MaxVersionVector(doc.ActorID()))
	assert.Equal(t, `{"arr":[1,3]}`, doc.Marshal())
	assert.Equal(t, 0, doc.GarbageLen())
}
