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

// TestReissueOperationsKeepsCounterHLL covers the wire round-trip losing a
// dedup Counter's HLL registers: JSONElementSimple, the message a Set carries
// its value in, has no field for them, so a value re-issued through it alone
// would come back as an empty sketch counting zero.
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
