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

package json_test

import (
	"math"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

func TestPrimitive(t *testing.T) {
	tests := []struct {
		value     interface{}
		valueType json.ValueType
		marshal   string
	}{
		{nil, json.Null, "null"},
		{false, json.Boolean, "false"},
		{true, json.Boolean, "true"},
		{0, json.Integer, "0"},
		{int64(0), json.Long, "0"},
		{float64(0), json.Double, "0.000000"},
		{"0", json.String, `"0"`},
		{[]byte{}, json.Bytes, `""`},
		{gotime.Unix(0, 0), json.Date, gotime.Unix(0, 0).Format(gotime.RFC3339)},
	}

	t.Run("creation and deep copy test", func(t *testing.T) {
		for _, test := range tests {
			prim := json.NewPrimitive(test.value, time.InitialTicket)
			assert.Equal(t, prim.ValueType(), test.valueType)
			assert.Equal(t, prim.Value(), json.ValueFromBytes(prim.ValueType(), prim.Bytes()))
			assert.Equal(t, prim.Marshal(), test.marshal)

			copied := prim.DeepCopy()
			assert.Equal(t, prim.CreatedAt(), copied.CreatedAt())
			assert.Equal(t, prim.MovedAt(), copied.MovedAt())
			assert.Equal(t, prim.Marshal(), copied.Marshal())

			actorID, _ := time.ActorIDFromHex("0")
			movedAt := time.NewTicket(0, 0, &actorID)
			prim.SetMovedAt(&movedAt)
			assert.NotEqual(t, prim.MovedAt(), copied.MovedAt())
		}
		longPrim := json.NewPrimitive(math.MaxInt32+1, time.InitialTicket)
		assert.Equal(t, longPrim.ValueType(), json.Long)
	})
}
