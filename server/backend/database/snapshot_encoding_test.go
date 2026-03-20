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

package database_test

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestSnapshotEncoding(t *testing.T) {
	t.Run("compress and decompress round-trip", func(t *testing.T) {
		original := []byte("hello world, this is a test snapshot payload")
		compressed, err := database.CompressSnapshot(original)
		assert.NoError(t, err)
		assert.NotEqual(t, original, compressed)

		decompressed, err := database.DecompressSnapshot(compressed)
		assert.NoError(t, err)
		assert.Equal(t, original, decompressed)
	})

	t.Run("decompress uncompressed data (backward compat)", func(t *testing.T) {
		raw := []byte{0x0a, 0x10, 0x20}
		result, err := database.DecompressSnapshot(raw)
		assert.NoError(t, err)
		assert.Equal(t, raw, result)
	})

	t.Run("decompress empty data", func(t *testing.T) {
		result, err := database.DecompressSnapshot(nil)
		assert.NoError(t, err)
		assert.Nil(t, result)

		result, err = database.DecompressSnapshot([]byte{})
		assert.NoError(t, err)
		assert.Equal(t, []byte{}, result)
	})

	t.Run("compressed data is smaller for repetitive content", func(t *testing.T) {
		original := bytes.Repeat([]byte("abcdefghij"), 10000)
		compressed, err := database.CompressSnapshot(original)
		assert.NoError(t, err)
		assert.Less(t, len(compressed), len(original))

		decompressed, err := database.DecompressSnapshot(compressed)
		assert.NoError(t, err)
		assert.Equal(t, original, decompressed)
	})
}
