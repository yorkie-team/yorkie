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
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestIncreaseRejectsNonFiniteDelta covers the two opposite jobs FromOperations
// has on an Increase carrying a NaN or an infinity. On the wire the delta is
// rejected, so it is never stored or broadcast: it has no integer value to add,
// every replica drops it (crdt.Counter.Increase) and the counter silently
// diverges from one that does not. On the stored paths -- the server's read
// (FromStoredOperations) and its forward to pulling clients
// (SanitizeStoredOperations) -- the same rejection would make a document
// written before the check permanently unloadable and unpullable, so the delta
// is repaired to zero instead, which is what it already did to every replica.
func TestIncreaseRejectsNonFiniteDelta(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000000")
	require.NoError(t, err)
	seed := time.NewTicket(1, 0, actor)
	executedAt := time.NewTicket(4, 0, actor)

	pbIncrease := func(t *testing.T, delta float64) []*api.Operation {
		t.Helper()

		value, err := crdt.NewPrimitive(delta, executedAt)
		require.NoError(t, err)
		pbOps, err := converter.ToOperations([]operations.Operation{
			operations.NewIncrease(seed, value, executedAt),
		})
		require.NoError(t, err)
		return pbOps
	}

	deltaOf := func(t *testing.T, ops []operations.Operation) any {
		t.Helper()

		require.Len(t, ops, 1)
		inc, ok := ops[0].(*operations.Increase)
		require.True(t, ok)
		primitive, ok := inc.Value().(*crdt.Primitive)
		require.True(t, ok)
		return primitive.Value()
	}

	for _, tc := range []struct {
		name  string
		delta float64
	}{
		{"nan", math.NaN()},
		{"positive infinity", math.Inf(1)},
		{"negative infinity", math.Inf(-1)},
	} {
		t.Run(tc.name+" test", func(t *testing.T) {
			// 01. the wire boundary rejects it.
			_, err := converter.FromOperations(pbIncrease(t, tc.delta))
			assert.ErrorIs(t, err, converter.ErrNonFiniteCounterDelta)

			// 02. the stored read decodes it as a zero delta rather than
			// failing, which would make the document holding it unloadable.
			ops, err := converter.FromStoredOperations(pbIncrease(t, tc.delta))
			require.NoError(t, err)
			assert.Equal(t, float64(0), deltaOf(t, ops))

			// 03. what the pull path forwards is accepted by the strict
			// decoder every client runs on what it receives.
			sanitized := converter.SanitizeStoredOperations(pbIncrease(t, tc.delta))
			ops, err = converter.FromOperations(sanitized)
			require.NoError(t, err)
			assert.Equal(t, float64(0), deltaOf(t, ops))
		})
	}

	t.Run("finite delta test", func(t *testing.T) {
		ops, err := converter.FromOperations(pbIncrease(t, 1.5))
		require.NoError(t, err)
		assert.Equal(t, 1.5, deltaOf(t, ops))

		ops, err = converter.FromStoredOperations(pbIncrease(t, 1.5))
		require.NoError(t, err)
		assert.Equal(t, 1.5, deltaOf(t, ops))
	})
}
