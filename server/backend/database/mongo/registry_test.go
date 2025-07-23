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
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestRegistry(t *testing.T) {
	registry := NewRegistryBuilder()

	t.Run("types.ID test", func(t *testing.T) {
		id := types.ID(bson.NewObjectID().Hex())

		buf := new(bytes.Buffer)
		encoder := bson.NewEncoder(bson.NewDocumentWriter(buf))
		encoder.SetRegistry(registry)
		err := encoder.Encode(bson.M{
			"_id": id,
		})
		assert.NoError(t, err)

		info := database.ClientInfo{}
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(buf.Bytes())))
		decoder.SetRegistry(registry)
		assert.NoError(t, decoder.Decode(&info))
		assert.Equal(t, id, info.ID)
	})

	t.Run("versionVector test", func(t *testing.T) {
		vector := time.NewVersionVector()
		actorID, err := time.ActorIDFromHex(bson.NewObjectID().Hex())
		assert.NoError(t, err)
		vector.Set(actorID, 1)

		buf := new(bytes.Buffer)
		encoder := bson.NewEncoder(bson.NewDocumentWriter(buf))
		encoder.SetRegistry(registry)
		err = encoder.Encode(bson.M{
			"version_vector": vector,
		})
		assert.NoError(t, err)

		info := struct {
			VersionVector time.VersionVector `bson:"version_vector"`
		}{}
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(buf.Bytes())))
		decoder.SetRegistry(registry)
		assert.NoError(t, decoder.Decode(&info))
		assert.Equal(t, vector, info.VersionVector)
	})

	t.Run("presenceChange test", func(t *testing.T) {
		presence := innerpresence.New()
		presence.Set("color", "orange")
		presenceChange := &innerpresence.Change{
			ChangeType: innerpresence.Put,
			Presence:   presence,
		}

		buf := new(bytes.Buffer)
		encoder := bson.NewEncoder(bson.NewDocumentWriter(buf))
		encoder.SetRegistry(registry)
		err := encoder.Encode(bson.M{
			"presence_change": presenceChange,
		})
		assert.NoError(t, err)

		info := struct {
			PresenceChange *innerpresence.Change `bson:"presence_change"`
		}{}
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(buf.Bytes())))
		decoder.SetRegistry(registry)
		assert.NoError(t, decoder.Decode(&info))

		assert.Equal(t, presenceChange, info.PresenceChange)
	})
}

func TestEncoder(t *testing.T) {
	t.Run("idEncoder test", func(t *testing.T) {
		registry := NewRegistryBuilder()
		field := "id"
		id := types.ID(bson.NewObjectID().Hex())

		buf := new(bytes.Buffer)
		encoder := bson.NewEncoder(bson.NewDocumentWriter(buf))
		encoder.SetRegistry(registry)
		err := encoder.Encode(bson.M{field: id})
		assert.NoError(t, err)

		result := make(map[string]string)
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(buf.Bytes())))
		decoder.ObjectIDAsHexString()
		assert.NoError(t, decoder.Decode(&result))
		assert.Equal(t, id.String(), result[field])
	})

	t.Run("actorIDEncoder test", func(t *testing.T) {
		registry := NewRegistryBuilder()
		field := "actor_id"
		actorID, err := time.ActorIDFromHex(bson.NewObjectID().Hex())
		assert.NoError(t, err)

		buf := new(bytes.Buffer)
		encoder := bson.NewEncoder(bson.NewDocumentWriter(buf))
		encoder.SetRegistry(registry)
		err = encoder.Encode(bson.M{field: actorID})
		assert.NoError(t, err)

		result := make(map[string]string)
		decoder := bson.NewDecoder(bson.NewDocumentReader(bytes.NewReader(buf.Bytes())))
		decoder.ObjectIDAsHexString()
		assert.NoError(t, decoder.Decode(&result))
		assert.Equal(t, actorID.String(), result[field])
	})
}
