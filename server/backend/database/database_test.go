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

package database_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestID(t *testing.T) {
	t.Run("get ID from hex test", func(t *testing.T) {
		str := "0123456789abcdef01234567"
		ID := types.ID(str)
		assert.Equal(t, str, ID.String())
	})

	t.Run("get ID from bytes test", func(t *testing.T) {
		bytes := make([]byte, 12)
		ID := types.IDFromBytes(bytes)
		bytesID, err := ID.Bytes()
		assert.NoError(t, err)
		assert.Equal(t, bytes, bytesID)
	})
}
