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

package crdt

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestZeroStyleRegionEncoding(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000003")
	require.NoError(t, err)
	until := NewRGATreeSplitNodeID(time.NewTicket(1, 1, actor), 8)
	region := &ZeroStyleRegion{
		Until: until,
		Writes: []ZeroAttrWrite{
			{Key: "style.bold", Value: "true"},
			{Key: "style.italic", Removed: true},
			{Key: "data.src", Value: `"<a&b>"`},
		},
		Ticket: time.NewTicket(9, 2, actor),
	}

	t.Run("encodes as the JS SDK does", func(t *testing.T) {
		// JSON.stringify({u: encodeNodeID(until), w: writes}) in the JS SDK
		assert.Equal(t,
			`{"u":"[\"1\",1,\"000000000000000000000003\",8]",`+
				`"w":[["style.bold","true"],["style.italic",null],["data.src","\"<a&b>\""]]}`,
			region.encode())
		assert.Equal(t, "zt.r.9:000000000000000000000003:2", region.key())
	})

	t.Run("decodes what it encodes", func(t *testing.T) {
		got, err := decodeZeroRegion(region.encode())
		require.NoError(t, err)
		assert.Equal(t, 0, got.Until.createdAt.Compare(until.createdAt))
		assert.Equal(t, until.offset, got.Until.offset)
		assert.Equal(t, region.Writes, got.Writes)
	})

	t.Run("decodes a range to the end of the document", func(t *testing.T) {
		got, err := decodeZeroRegion(`{"u":"","w":[["style.bold","true"]]}`)
		require.NoError(t, err)
		assert.Nil(t, got.Until)
	})
}
