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

package types_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
)

func TestClientRefKey(t *testing.T) {
	refKey := types.ClientRefKey{
		ProjectID: types.ID("000000000000000000000000"),
		ClientID:  types.ID("000000000000000000000001"),
	}

	t.Run("cache key round-trip test", func(t *testing.T) {
		// A client cache invalidation names the client by this key, so a key
		// that does not round-trip silently drops the row it should evict.
		parsed, err := types.ParseClientRefKey(refKey.CacheKey())
		assert.NoError(t, err)
		assert.Equal(t, refKey, parsed)
	})

	t.Run("malformed cache key test", func(t *testing.T) {
		for _, cacheKey := range []string{
			"",
			"000000000000000000000000",
			"/000000000000000000000001",
			"000000000000000000000000/",
			"not-an-id/000000000000000000000001",
			"000000000000000000000000/not-an-id",
		} {
			_, err := types.ParseClientRefKey(cacheKey)
			assert.Error(t, err, "cache key: %q", cacheKey)
		}
	})
}
