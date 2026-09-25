/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package time_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestActorID(t *testing.T) {
	t.Run("get ActorID from hex test", func(t *testing.T) {
		actorID := time.ActorID{}
		_, err := rand.Read(actorID.Bytes())
		assert.NoError(t, err)

		expectedID := actorID.String()
		actualID, err := time.ActorIDFromHex(expectedID)

		assert.NoError(t, err)
		assert.Equal(t, expectedID, actualID.String())
	})

	t.Run("get ActorID from hex on invalid hexadecimal string test", func(t *testing.T) {
		testID := "testID"
		_, err := time.ActorIDFromHex(testID)
		assert.ErrorIs(t, err, time.ErrInvalidHexString)
	})

	t.Run("get ActorID from hex on invalid decoded length test", func(t *testing.T) {
		bytes := make([]byte, 5)
		_, err := rand.Read(bytes)
		assert.NoError(t, err)

		invalidID := hex.EncodeToString(bytes[:])
		_, err = time.ActorIDFromHex(invalidID)
		assert.ErrorIs(t, err, time.ErrInvalidHexString)
	})

	t.Run("get ActorID from bytes test", func(t *testing.T) {
		actorID := time.ActorID{}
		_, err := rand.Read(actorID.Bytes())
		assert.NoError(t, err)

		expectedBytes := actorID.Bytes()
		actualID, err := time.ActorIDFromBytes(expectedBytes)

		assert.NoError(t, err)
		assert.Equal(t, expectedBytes, actualID.Bytes())
	})

	t.Run("get ActorID from bytes on invalid length test", func(t *testing.T) {
		invalidBytes := make([]byte, 5)
		_, err := time.ActorIDFromBytes(invalidBytes)
		assert.ErrorIs(t, err, time.ErrInvalidActorID)
	})
}
