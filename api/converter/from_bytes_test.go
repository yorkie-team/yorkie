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

package converter_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
)

// validTicket returns a well-formed TimeTicket (ActorID must be exactly 12 bytes).
func validTicket() *api.TimeTicket {
	return &api.TimeTicket{ActorId: make([]byte, 12)}
}

// nilCreatedAtPrimitive is a primitive element whose createdAt is unset.
func nilCreatedAtPrimitive() *api.JSONElement {
	return &api.JSONElement{
		Body: &api.JSONElement_Primitive_{
			Primitive: &api.JSONElement_Primitive{
				Type:      api.ValueType_VALUE_TYPE_NULL,
				CreatedAt: nil,
			},
		},
	}
}

// TestBytesToObjectMalformed verifies BytesToObject returns an error (never
// panics) on malformed snapshots that proto.Unmarshal still accepts.
func TestBytesToObjectMalformed(t *testing.T) {
	tests := []struct {
		name string
		elem *api.JSONElement
	}{
		{
			name: "mismatched body (no json object)",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonArray{
				JsonArray: &api.JSONElement_JSONArray{CreatedAt: validTicket()},
			}},
		},
		{
			name: "member with nil element",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonObject{
				JsonObject: &api.JSONElement_JSONObject{
					CreatedAt: validTicket(),
					Nodes:     []*api.RHTNode{{Key: "k", Element: nil}},
				},
			}},
		},
		{
			name: "member with nil createdAt",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonObject{
				JsonObject: &api.JSONElement_JSONObject{
					CreatedAt: validTicket(),
					Nodes:     []*api.RHTNode{{Key: "k", Element: nilCreatedAtPrimitive()}},
				},
			}},
		},
		{
			name: "member with nil oneof body",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonObject{
				JsonObject: &api.JSONElement_JSONObject{
					CreatedAt: validTicket(),
					Nodes:     []*api.RHTNode{{Key: "k", Element: &api.JSONElement{}}},
				},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := proto.Marshal(tc.elem)
			assert.NoError(t, err)

			obj, err := converter.BytesToObject(data)
			assert.Error(t, err)
			assert.Nil(t, obj)
		})
	}
}

// TestBytesToArrayMalformed verifies BytesToArray returns an error (never
// panics) on malformed snapshots that proto.Unmarshal still accepts.
func TestBytesToArrayMalformed(t *testing.T) {
	tests := []struct {
		name string
		elem *api.JSONElement
	}{
		{
			name: "mismatched body (no json array)",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonObject{
				JsonObject: &api.JSONElement_JSONObject{CreatedAt: validTicket()},
			}},
		},
		{
			name: "element with nil createdAt",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonArray{
				JsonArray: &api.JSONElement_JSONArray{
					CreatedAt: validTicket(),
					Nodes:     []*api.RGANode{{Element: nilCreatedAtPrimitive()}},
				},
			}},
		},
		{
			name: "nil element without position timestamps",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonArray{
				JsonArray: &api.JSONElement_JSONArray{
					CreatedAt: validTicket(),
					Nodes:     []*api.RGANode{{Element: nil}},
				},
			}},
		},
		{
			name: "element with nil oneof body",
			elem: &api.JSONElement{Body: &api.JSONElement_JsonArray{
				JsonArray: &api.JSONElement_JSONArray{
					CreatedAt: validTicket(),
					Nodes:     []*api.RGANode{{Element: &api.JSONElement{}}},
				},
			}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data, err := proto.Marshal(tc.elem)
			assert.NoError(t, err)

			arr, err := converter.BytesToArray(data)
			assert.Error(t, err)
			assert.Nil(t, arr)
		})
	}
}
