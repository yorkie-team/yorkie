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

package innerpresence_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
)

func TestChangeMarshalUnmarshal(t *testing.T) {
	t.Run("should marshal and unmarshal Put type change", func(t *testing.T) {
		presence := make(map[string]string)
		presence["cursor"] = "10,20"
		presence["color"] = "red"
		presence["name"] = "user1"

		original := &innerpresence.Change{
			ChangeType: innerpresence.Put,
			Presence:   presence,
		}

		// Marshal
		data, err := original.Bytes()
		assert.NoError(t, err)
		assert.NotNil(t, data)

		// Unmarshal
		decoded, err := innerpresence.ChangeFromBytes(data)
		assert.NoError(t, err)

		// Compare
		assert.Equal(t, original.ChangeType, decoded.ChangeType)
		assert.Equal(t, original.Presence, decoded.Presence)
	})

	t.Run("should marshal and unmarshal Clear type change", func(t *testing.T) {
		original := &innerpresence.Change{
			ChangeType: innerpresence.Clear,
			Presence:   nil,
		}

		// Marshal
		data, err := original.Bytes()
		assert.NoError(t, err)
		assert.NotNil(t, data)

		// Unmarshal
		decoded, err := innerpresence.ChangeFromBytes(data)
		assert.NoError(t, err)

		// Compare
		assert.Equal(t, original.ChangeType, decoded.ChangeType)
		assert.Nil(t, decoded.Presence)
	})

	t.Run("should handle empty presence", func(t *testing.T) {
		original := &innerpresence.Change{
			ChangeType: innerpresence.Put,
			Presence:   make(map[string]string),
		}

		// Marshal
		data, err := original.Bytes()
		assert.NoError(t, err)
		assert.NotNil(t, data)

		// Unmarshal
		decoded, err := innerpresence.ChangeFromBytes(data)
		assert.NoError(t, err)

		// Compare
		assert.Equal(t, original.ChangeType, decoded.ChangeType)
		assert.Equal(t, original.Presence, decoded.Presence)
	})

	t.Run("should handle nil change", func(t *testing.T) {
		var original *innerpresence.Change

		// Marshal
		data, err := original.Bytes()
		assert.NoError(t, err)
		assert.Nil(t, data)
	})
}

func TestFromBytes(t *testing.T) {
	t.Run("should create change from bytes", func(t *testing.T) {
		presence := make(map[string]string)
		presence["key"] = "value"

		original := &innerpresence.Change{
			ChangeType: innerpresence.Put,
			Presence:   presence,
		}

		// Marshal
		data, err := original.Bytes()
		assert.NoError(t, err)

		// FromBytes
		decoded, err := innerpresence.ChangeFromBytes(data)
		assert.NoError(t, err)
		assert.NotNil(t, decoded)

		// Compare
		assert.Equal(t, original.ChangeType, decoded.ChangeType)
		assert.Equal(t, original.Presence, decoded.Presence)
	})

	t.Run("should handle empty bytes", func(t *testing.T) {
		decoded, err := innerpresence.ChangeFromBytes(nil)
		assert.NoError(t, err)
		assert.Nil(t, decoded)
	})

	t.Run("should handle invalid data", func(t *testing.T) {
		invalidData := []byte{99} // invalid change type

		_, err := innerpresence.ChangeFromBytes(invalidData)
		assert.Error(t, err)
	})
}

func TestChangeFromBytes_ErrorCases(t *testing.T) {
	t.Run("should return error for empty data when not expected", func(t *testing.T) {
		_, err := innerpresence.ChangeFromBytes([]byte{})
		assert.NoError(t, err) // Empty data is valid (returns nil)
	})

	t.Run("should return error for truncated data", func(t *testing.T) {
		invalidData := []byte{innerpresence.ChangeTypePutByte} // Put type but missing presence count

		_, err := innerpresence.ChangeFromBytes(invalidData)
		assert.Error(t, err)
	})

	t.Run("should return error for invalid change type", func(t *testing.T) {
		invalidData := []byte{99} // invalid change type

		_, err := innerpresence.ChangeFromBytes(invalidData)
		assert.Error(t, err)
	})
}
