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
	"testing"

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
		{0, json.Integer, "0"},
		{"0", json.String, `"0"`},
		{[]byte{}, json.Bytes, `""`},
	}

	t.Run("creation test", func(t *testing.T) {
		for _, test := range tests {
			prim := json.NewPrimitive(test.value, time.InitialTicket)
			assert.Equal(t, prim.ValueType(), prim.ValueType())
			assert.Equal(t, prim.Value(), json.ValueFromBytes(prim.ValueType(), prim.Bytes()))
			assert.Equal(t, prim.Marshal(), test.marshal)
		}
	})
}
