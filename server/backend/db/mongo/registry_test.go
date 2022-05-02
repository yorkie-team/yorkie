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

package mongo

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend/db"
)

func TestRegistry(t *testing.T) {
	registry := newRegistryBuilder().Build()

	id := types.ID(primitive.NewObjectID().Hex())
	data, err := bson.MarshalWithRegistry(registry, bson.M{
		"_id": id,
	})
	assert.NoError(t, err)

	info := db.ClientInfo{}
	assert.NoError(t, bson.UnmarshalWithRegistry(registry, data, &info))
	assert.Equal(t, id, info.ID)

}
