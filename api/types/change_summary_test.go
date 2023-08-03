/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

func TestChangeSummary(t *testing.T) {
	t.Run("get changes range test", func(t *testing.T) {
		lastSeq := int64(10)
		var from, to int64

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    0,
			PageSize:  0,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    0,
			PageSize:  0,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  3,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(3), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  3,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, int64(5), from)
		assert.Equal(t, int64(7), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  100,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(3), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  100,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, int64(5), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  0,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(3), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    4,
			PageSize:  0,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, int64(5), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    0,
			PageSize:  2,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(9), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    0,
			PageSize:  2,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, int64(1), from)
		assert.Equal(t, int64(2), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    14,
			PageSize:  3,
			IsForward: false,
		}, lastSeq)
		assert.Equal(t, int64(8), from)
		assert.Equal(t, int64(10), to)

		from, to = types.GetChangesRange(types.Paging[int64]{
			Offset:    14,
			PageSize:  3,
			IsForward: true,
		}, lastSeq)
		assert.Equal(t, lastSeq+1, from)
		assert.Equal(t, lastSeq+1, to)
	})
}
