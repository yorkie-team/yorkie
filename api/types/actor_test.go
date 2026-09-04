/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestDeriveActorID(t *testing.T) {
	proj := types.ID("000000000000000000000000")
	other := types.ID("0123456789abcdef01234567")

	t.Run("determinism test", func(t *testing.T) {
		first := types.DeriveActorID(proj, "alice")
		second := types.DeriveActorID(proj, "alice")
		assert.Equal(t, first, second)
	})

	t.Run("different client key yields different actor test", func(t *testing.T) {
		alice := types.DeriveActorID(proj, "alice")
		bob := types.DeriveActorID(proj, "bob")
		assert.NotEqual(t, alice, bob)
	})

	t.Run("different project namespaces the actor test", func(t *testing.T) {
		here := types.DeriveActorID(proj, "alice")
		there := types.DeriveActorID(other, "alice")
		assert.NotEqual(t, here, there)
	})

	t.Run("output is a valid 12-byte actor id test", func(t *testing.T) {
		actor := types.DeriveActorID(proj, "alice")
		assert.NoError(t, actor.Validate())

		actorID, err := actor.ToActorID()
		assert.NoError(t, err)
		assert.Len(t, actorID.Bytes(), 12)
	})

	t.Run("never equals reserved values test", func(t *testing.T) {
		initial := types.IDFromActorID(time.InitialActorID)
		max := types.IDFromActorID(time.MaxActorID)

		// Sweep a range of keys to exercise the reserved-value guard path; none
		// of the derived actors may collide with the two reserved values.
		for _, key := range []string{"", "alice", "bob", "carol", "dave", "eve"} {
			actor := types.DeriveActorID(proj, key)
			assert.NotEqual(t, initial, actor)
			assert.NotEqual(t, max, actor)
		}
	})

	t.Run("pinned test vectors test", func(t *testing.T) {
		// Pinned outputs of the permanent "yorkie/stable-actor/v1" derivation.
		// A change here means the algorithm changed and every persisted offline
		// document is orphaned, so these must not drift silently.
		assert.Equal(t, types.ID("c2c36be133e39f90dc17d198"), types.DeriveActorID(proj, "alice"))
		assert.Equal(t, types.ID("06631cb05238779c0597f815"), types.DeriveActorID(proj, "bob"))
		assert.Equal(t, types.ID("479dc734ba367130cd706e15"), types.DeriveActorID(other, "alice"))
	})
}
