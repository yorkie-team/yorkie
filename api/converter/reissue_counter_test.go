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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// dedupCounter returns a dedup Counter whose sketch counts the given voters,
// the state a document seeded from YSON holds before any Increase replays.
func dedupCounter(t *testing.T, actor time.ActorID, voters ...string) *crdt.Counter {
	t.Helper()

	one, err := crdt.NewPrimitive(int32(1), time.NewTicket(1, 0, actor))
	require.NoError(t, err)
	counter, err := crdt.NewCounter(crdt.IntegerDedupCnt, int32(0), time.NewTicket(2, 0, actor))
	require.NoError(t, err)
	for _, voter := range voters {
		_, err := counter.IncreaseDedup(one, voter)
		require.NoError(t, err)
	}
	require.NotEmpty(t, counter.HLLBytes())
	return counter
}

// TestOperationsKeepCounterHLLOnWire covers the push path, not the re-issue:
// every Set/Add/ArraySet that leaves this replica -- the change pack a client
// pushes, the compacted changes the server stores -- carries its value in a
// JSONElementSimple, and a dedup counter's value is derived from its sketch.
// Without the registers alongside it the peer rebuilds an empty sketch and
// reads the counter as zero, which for a push is a divergence from the
// pushing replica and for compaction is silent data loss.
func TestOperationsKeepCounterHLLOnWire(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	counter := dedupCounter(t, actor, "a", "b", "c")
	require.Equal(t, int32(3), counter.Value())

	executedAt := time.NewTicket(3, 0, actor)
	for name, op := range map[string]operations.Operation{
		"set":      operations.NewSet(time.InitialTicket, "cnt", counter, executedAt),
		"add":      operations.NewAdd(time.InitialTicket, time.InitialTicket, counter, executedAt),
		"arraySet": operations.NewArraySet(time.InitialTicket, counter.CreatedAt(), counter, executedAt),
	} {
		t.Run(name, func(t *testing.T) {
			pbOps, err := converter.ToOperations([]operations.Operation{op})
			require.NoError(t, err)
			decoded, err := converter.FromOperations(pbOps)
			require.NoError(t, err)
			require.Len(t, decoded, 1)

			var value crdt.Element
			switch o := decoded[0].(type) {
			case *operations.Set:
				value = o.Value()
			case *operations.Add:
				value = o.Value()
			case *operations.ArraySet:
				value = o.Value()
			}
			cnt, ok := value.(*crdt.Counter)
			require.True(t, ok)
			assert.Equal(t, int32(3), cnt.Value(), "dedup counter value survives the wire")
			assert.Equal(t, counter.HLLBytes(), cnt.HLLBytes(), "HLL registers survive the wire")
		})
	}
}

// TestReissueOperationsKeepsCounterHLL covers the wire round-trip keeping a
// dedup Counter's HLL registers through the re-issue, which goes through the
// very encoding the push path uses: a sketch lost here would be replayed into
// the local root as a counter reading zero.
func TestReissueOperationsKeepsCounterHLL(t *testing.T) {
	from, err := time.ActorIDFromHex("000000000000000000000001")
	require.NoError(t, err)
	to, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	one, err := crdt.NewPrimitive(int32(1), time.NewTicket(1, 0, from))
	require.NoError(t, err)

	counter, err := crdt.NewCounter(crdt.IntegerDedupCnt, int32(0), time.NewTicket(2, 0, from))
	require.NoError(t, err)
	for _, voter := range []string{"a", "b", "c"} {
		_, err := counter.IncreaseDedup(one, voter)
		require.NoError(t, err)
	}
	require.Equal(t, int32(3), counter.Value())
	require.NotEmpty(t, counter.HLLBytes())

	set := operations.NewSet(time.InitialTicket, "cnt", counter, time.NewTicket(3, 0, from))
	reissued, err := converter.ReissueOperations([]operations.Operation{set}, from, to)
	require.NoError(t, err)
	require.Len(t, reissued, 1)

	value := reissued[0].(*operations.Set).Value()
	cnt, ok := value.(*crdt.Counter)
	require.True(t, ok)
	assert.Equal(t, int32(3), cnt.Value(), "dedup counter value survives the re-issue")
	assert.Equal(t, counter.HLLBytes(), cnt.HLLBytes(), "HLL registers survive the re-issue")
	assert.Equal(t, to, cnt.CreatedAt().ActorID())
	assert.Equal(t, to, reissued[0].ExecutedAt().ActorID())
}
