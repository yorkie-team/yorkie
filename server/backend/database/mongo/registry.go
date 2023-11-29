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
	"fmt"
	"reflect"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsoncodec"
	"go.mongodb.org/mongo-driver/bson/bsonoptions"
	"go.mongodb.org/mongo-driver/bson/bsonrw"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

// NewRegistryBuilder returns a new registry builder with the default encoder and decoder.
func NewRegistryBuilder() *bsoncodec.RegistryBuilder {
	rb := bsoncodec.NewRegistryBuilder()

	bsoncodec.DefaultValueEncoders{}.RegisterDefaultEncoders(rb)
	bsoncodec.DefaultValueDecoders{}.RegisterDefaultDecoders(rb)
	bson.PrimitiveCodecs{}.RegisterPrimitiveCodecs(rb)

	rb.RegisterCodec(
		reflect.TypeOf(types.ID("")),
		bsoncodec.NewStringCodec(bsonoptions.StringCodec().SetDecodeObjectIDAsHex(true)),
	)

	// Register a decoder that converts the `documents` field in the clients collection
	// into `database.ClientDocInfo.Documents`. The `documents` field is a two level map
	// containing a number of `doc_key`.`doc_id`.{`client_seq`, `server_seq`, `status`}s.
	rb.RegisterTypeDecoder(
		reflect.TypeOf(make(database.ClientDocInfoMap)),
		bsoncodec.ValueDecoderFunc(func(_ bsoncodec.DecodeContext, vr bsonrw.ValueReader, val reflect.Value) error {
			docs, err := vr.ReadDocument()
			if err != nil {
				return fmt.Errorf("read documents: %w", err)
			}
			if val.IsNil() {
				val.Set(reflect.MakeMap(val.Type()))
			}

			for {
				docKey, docInfoByDocIDMapReader, err := docs.ReadElement()
				if err != nil {
					if err == bsonrw.ErrEOD {
						break
					}
					return fmt.Errorf("read the element in documents: %w", err)
				}
				docInfoByDocIDMap, err := docInfoByDocIDMapReader.ReadDocument()
				if err != nil {
					return fmt.Errorf("read docInfoByDocID: %w", err)
				}
				for {
					docID, docInfoReader, err := docInfoByDocIDMap.ReadElement()
					if err != nil {
						if err == bsonrw.ErrEOD {
							break
						}
						return fmt.Errorf("read the element in docInfoByDocID: %w", err)
					}

					docInfo := &database.ClientDocInfo{}
					docInfoDecoder, err := bson.NewDecoder(docInfoReader)
					if err != nil {
						return fmt.Errorf("create docInfoDecoder: %w", err)
					}
					err = docInfoDecoder.Decode(docInfo)
					if err != nil {
						return fmt.Errorf("decode docInfo: %w", err)
					}

					docRef := reflect.ValueOf(types.DocRefKey{
						Key: key.Key(docKey),
						ID:  types.ID(docID),
					})
					val.SetMapIndex(docRef, reflect.ValueOf(docInfo))
				}
			}

			return nil
		}))

	return rb
}
