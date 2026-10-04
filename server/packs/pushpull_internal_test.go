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

package packs

import (
	"encoding/binary"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// actorIDAt builds a distinct actor id from a counter, so a test can fill a
// version vector with many unrelated actors the way a forged pack would.
func actorIDAt(t *testing.T, i int) time.ActorID {
	t.Helper()

	var raw [12]byte
	binary.BigEndian.PutUint64(raw[4:], uint64(i)+1)
	actorID, err := time.ActorIDFromBytes(raw[:])
	assert.NoError(t, err)
	return actorID
}

func versionVectorOfSize(t *testing.T, n int) time.VersionVector {
	t.Helper()

	vector := time.NewVersionVector()
	for i := range n {
		vector.Set(actorIDAt(t, i), int64(i)+1)
	}
	assert.Len(t, vector, n)
	return vector
}

func TestValidateVersionVectorSize(t *testing.T) {
	t.Run("a vector at the limit is accepted", func(t *testing.T) {
		pack := &change.Pack{VersionVector: versionVectorOfSize(t, maxVersionVectorEntries)}
		assert.NoError(t, validateVersionVectorSize(pack))
	})

	t.Run("an empty or absent vector is accepted", func(t *testing.T) {
		assert.NoError(t, validateVersionVectorSize(&change.Pack{}))
		assert.NoError(t, validateVersionVectorSize(&change.Pack{
			VersionVector: time.NewVersionVector(),
		}))
	})

	// The forged case: ChangePack.VersionVector is client-supplied and its
	// membership cannot be checked (a vector legitimately carries other
	// actors' lamports), so nothing but this gate bounds how many entries a
	// single pack plants in the pusher's VersionVectorInfo row, the
	// per-document vectorCache and the minVV returned to every other client.
	t.Run("a vector past the limit is refused before anything is stored", func(t *testing.T) {
		pack := &change.Pack{VersionVector: versionVectorOfSize(t, maxVersionVectorEntries+1)}
		err := validateVersionVectorSize(pack)
		assert.Error(t, err)
		assert.Equal(t, connect.CodeInvalidArgument, connect.CodeOf(err))
		assert.Contains(t, err.Error(), "exceeding the limit")
	})
}
