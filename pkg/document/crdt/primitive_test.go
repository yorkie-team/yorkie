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

package crdt_test

import (
	"fmt"
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestPrimitive(t *testing.T) {
	tests := []struct {
		value     interface{}
		valueType crdt.ValueType
		marshal   string
	}{
		{nil, crdt.Null, "null"},
		{false, crdt.Boolean, "false"},
		{true, crdt.Boolean, "true"},
		{0, crdt.Integer, "0"},
		{int32(0), crdt.Integer, "0"},
		{int64(0), crdt.Long, "0"},
		{float64(0), crdt.Double, "0.000000"},
		{"0", crdt.String, `"0"`},
		{[]byte{}, crdt.Bytes, `""`},
		{gotime.Unix(0, 0), crdt.Date, fmt.Sprintf(`"%s"`, gotime.Unix(0, 0).Format(gotime.RFC3339))},
	}

	t.Run("creation and deep copy test", func(t *testing.T) {
		for _, test := range tests {
			prim, err := crdt.NewPrimitive(test.value, time.InitialTicket)
			assert.NoError(t, err)
			assert.Equal(t, prim.ValueType(), test.valueType)
			value, err := crdt.ValueFromBytes(prim.ValueType(), prim.Bytes())
			assert.NoError(t, err)
			assert.Equal(t, prim.Value(), value)
			assert.Equal(t, prim.Marshal(), test.marshal)

			copied, err := prim.DeepCopy()
			assert.NoError(t, err)
			assert.Equal(t, prim.CreatedAt(), copied.CreatedAt())
			assert.Equal(t, prim.MovedAt(), copied.MovedAt())
			assert.Equal(t, prim.Marshal(), copied.Marshal())

			actorID, _ := time.ActorIDFromHex("0")
			prim.SetMovedAt(time.NewTicket(0, 0, actorID))
			assert.NotEqual(t, prim.MovedAt(), copied.MovedAt())
		}
		longPrim, err := crdt.NewPrimitive(math.MaxInt32+1, time.InitialTicket)
		assert.NoError(t, err)
		assert.Equal(t, longPrim.ValueType(), crdt.Long)
	})
}
