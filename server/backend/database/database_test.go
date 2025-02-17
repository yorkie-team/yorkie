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
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/test/helper"
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

func TestFindMinVersionVector(t *testing.T) {
	actor1, _ := time.ActorIDFromHex("000000000000000000000001")
	actor2, _ := time.ActorIDFromHex("000000000000000000000002")
	actor3, _ := time.ActorIDFromHex("000000000000000000000003")

	tests := []struct {
		name            string
		vvInfos         []database.VersionVectorInfo
		excludeClientID types.ID
		expect          time.VersionVector
	}{
		{
			name:            "empty version vector infos",
			vvInfos:         []database.VersionVectorInfo{},
			excludeClientID: "",
			expect:          nil,
		},
		{
			name: "single version vector info",
			vvInfos: []database.VersionVectorInfo{
				{
					ClientID: "client1",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 5,
						actor2: 3,
					}),
				},
			},
			excludeClientID: "",
			expect: helper.VersionVectorOf(map[*time.ActorID]int64{
				actor1: 5,
				actor2: 3,
			}),
		},
		{
			name: "exclude client",
			vvInfos: []database.VersionVectorInfo{
				{
					ClientID: "client1",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 5,
						actor2: 3,
					}),
				},
				{
					ClientID: "client2",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 3,
						actor2: 4,
					}),
				},
			},
			excludeClientID: "client1",
			expect: helper.VersionVectorOf(map[*time.ActorID]int64{
				actor1: 3,
				actor2: 4,
			}),
		},
		{
			name: "exclude all clients",
			vvInfos: []database.VersionVectorInfo{
				{
					ClientID: "client1",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 5,
					}),
				},
			},
			excludeClientID: "client1",
			expect:          nil,
		},
		{
			name: "multiple clients with different actors",
			vvInfos: []database.VersionVectorInfo{
				{
					ClientID: "client1",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 5,
						actor2: 3,
					}),
				},
				{
					ClientID: "client2",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor2: 2,
						actor3: 4,
					}),
				},
			},
			excludeClientID: "",
			expect: helper.VersionVectorOf(map[*time.ActorID]int64{
				actor1: 0,
				actor2: 2,
				actor3: 0,
			}),
		},
		{
			name: "all clients have same actors",
			vvInfos: []database.VersionVectorInfo{
				{
					ClientID: "client1",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 5,
						actor2: 3,
					}),
				},
				{
					ClientID: "client2",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 3,
						actor2: 4,
					}),
				},
				{
					ClientID: "client3",
					VersionVector: helper.VersionVectorOf(map[*time.ActorID]int64{
						actor1: 4,
						actor2: 2,
					}),
				},
			},
			excludeClientID: "",
			expect: helper.VersionVectorOf(map[*time.ActorID]int64{
				actor1: 3,
				actor2: 2,
			}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := database.FindMinVersionVector(tc.vvInfos, tc.excludeClientID)
			assert.Equal(t, tc.expect, result)
		})
	}
}
