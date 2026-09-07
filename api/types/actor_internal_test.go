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

package types

import (
	"crypto/sha256"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestResolveReserved exercises the reserved-value guard directly, since a
// derived actor colliding with a reserved value organically has probability
// ~2^-96 and cannot be reached through DeriveActorID in a test.
func TestResolveReserved(t *testing.T) {
	seed := sha256.Sum256([]byte("seed"))

	t.Run("rehashes away from InitialActorID test", func(t *testing.T) {
		got := resolveReserved(time.InitialActorID, seed[:])
		assert.NotEqual(t, IDFromActorID(time.InitialActorID), got)
		assert.NotEqual(t, IDFromActorID(time.MaxActorID), got)
	})

	t.Run("rehashes away from MaxActorID test", func(t *testing.T) {
		got := resolveReserved(time.MaxActorID, seed[:])
		assert.NotEqual(t, IDFromActorID(time.InitialActorID), got)
		assert.NotEqual(t, IDFromActorID(time.MaxActorID), got)
	})

	t.Run("passes through a non-reserved actor test", func(t *testing.T) {
		actor := [actorIDSize]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
		assert.Equal(t, IDFromBytes(actor[:]), resolveReserved(actor, seed[:]))
	})
}
