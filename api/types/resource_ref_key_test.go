/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

func TestResourceRefKey(t *testing.T) {
	t.Run("DocRefKey Set test", func(t *testing.T) {
		docKey := key.Key("docKey")
		docID := types.ID("docID")
		docRef := types.DocRefKey{}

		// 01. Give an invalid input to Set.
		err := docRef.Set("abc")
		assert.ErrorIs(t, err, types.ErrInvalidDocRefKeyStringFormat)

		// 02. Give a valid input to Set.
		err = docRef.Set(fmt.Sprintf("%s,%s", docKey, docID))
		assert.NoError(t, err)
		assert.Equal(t, docKey, docRef.Key)
		assert.Equal(t, docID, docRef.ID)
	})
}
