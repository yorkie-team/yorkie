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
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/bson/bsonrw"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

func TestRegistry(t *testing.T) {
	registry := NewRegistryBuilder().Build()

	id := types.ID(primitive.NewObjectID().Hex())
	data, err := bson.MarshalWithRegistry(registry, bson.M{
		"_id": id,
	})
	assert.NoError(t, err)

	info := database.ClientInfo{}
	assert.NoError(t, bson.UnmarshalWithRegistry(registry, data, &info))
	assert.Equal(t, id, info.ID)

}

func TestEncoder(t *testing.T) {
	t.Run("idEncoder test", func(t *testing.T) {
		field := "id"
		id := types.ID(primitive.NewObjectID().Hex())

		buf := new(bytes.Buffer)
		vw, err := bsonrw.NewBSONValueWriter(buf)
		assert.NoError(t, err)
		dw, err := vw.WriteDocument()
		assert.NoError(t, err)
		vw, err = dw.WriteDocumentElement(field)
		assert.NoError(t, err)

		assert.NoError(t, idEncoder(bsoncodec.EncodeContext{}, vw, reflect.ValueOf(id)))
		assert.NoError(t, dw.WriteDocumentEnd())
		result := make(map[string]string)
		assert.NoError(t, bson.Unmarshal(buf.Bytes(), &result))
		assert.Equal(t, id.String(), result[field])
	})

	t.Run("actorIDEncoder test", func(t *testing.T) {
		field := "actor_id"
		actorID, err := time.ActorIDFromHex(primitive.NewObjectID().Hex())
		assert.NoError(t, err)

		buf := new(bytes.Buffer)
		vw, err := bsonrw.NewBSONValueWriter(buf)
		assert.NoError(t, err)
		dw, err := vw.WriteDocument()
		assert.NoError(t, err)
		vw, err = dw.WriteDocumentElement(field)
		assert.NoError(t, err)

		assert.NoError(t, actorIDEncoder(bsoncodec.EncodeContext{}, vw, reflect.ValueOf(actorID)))
		assert.NoError(t, dw.WriteDocumentEnd())
		result := make(map[string]string)
		assert.NoError(t, bson.Unmarshal(buf.Bytes(), &result))
		assert.Equal(t, actorID.String(), result[field])
	})
}

func TestDecoder(t *testing.T) {
	t.Run("clientDocumentsDecoder test", func(t *testing.T) {
		docs := []struct {
			docRefKey types.DocRefKey
			docInfo   database.ClientDocInfo
		}{
			{
				docRefKey: types.DocRefKey{
					Key: key.Key("test-doc-key1"),
					ID:  types.ID("test-doc-id1"),
				},
				docInfo: database.ClientDocInfo{
					ClientSeq: 0,
					ServerSeq: 0,
					Status:    database.DocumentAttached,
				},
			},
			{
				docRefKey: types.DocRefKey{
					Key: key.Key("test-doc-key2"),
					ID:  types.ID("test-doc-id2"),
				},
				docInfo: database.ClientDocInfo{
					ClientSeq: 0,
					ServerSeq: 0,
					Status:    database.DocumentDetached,
				},
			},
		}

		bsonDocs := make(bson.M)
		for _, doc := range docs {
			bsonDocs[doc.docRefKey.Key.String()] = bson.M{
				doc.docRefKey.ID.String(): bson.M{
					"client_seq": doc.docInfo.ClientSeq,
					"server_seq": doc.docInfo.ServerSeq,
					"status":     doc.docInfo.Status,
				},
			}
		}

		marshaledDocs, err := bson.Marshal(bsonDocs)
		assert.NoError(t, err)

		clientDocInfoMap := make(database.ClientDocInfoMap)
		err = clientDocumentsDecoder(
			bsoncodec.DecodeContext{},
			bsonrw.NewBSONDocumentReader(marshaledDocs),
			reflect.ValueOf(clientDocInfoMap),
		)
		assert.NoError(t, err)
		assert.Len(t, clientDocInfoMap, len(docs))
		for _, doc := range docs {
			assert.Equal(t, doc.docInfo.ClientSeq, clientDocInfoMap[doc.docRefKey].ClientSeq)
			assert.Equal(t, doc.docInfo.ServerSeq, clientDocInfoMap[doc.docRefKey].ServerSeq)
			assert.Equal(t, doc.docInfo.Status, clientDocInfoMap[doc.docRefKey].Status)
		}
	})
}
