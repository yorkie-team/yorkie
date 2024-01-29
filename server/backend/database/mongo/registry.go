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
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var tID = reflect.TypeOf(types.ID(""))
var tActorID = reflect.TypeOf(&time.ActorID{})
var tClientDocInfoMap = reflect.TypeOf(make(database.ClientDocInfoMap))

// NewRegistryBuilder returns a new registry builder with the default encoder and decoder.
func NewRegistryBuilder() *bsoncodec.RegistryBuilder {
	rb := bsoncodec.NewRegistryBuilder()

	bsoncodec.DefaultValueEncoders{}.RegisterDefaultEncoders(rb)
	bsoncodec.DefaultValueDecoders{}.RegisterDefaultDecoders(rb)
	bson.PrimitiveCodecs{}.RegisterPrimitiveCodecs(rb)

	// Register the decoder for ObjectID
	rb.RegisterCodec(
		tID,
		bsoncodec.NewStringCodec(bsonoptions.StringCodec().SetDecodeObjectIDAsHex(true)),
	)

	// Register the encoder for types.ID
	rb.RegisterTypeEncoder(tID, bsoncodec.ValueEncoderFunc(idEncoder))
	// Register the encoder for time.ActorID
	rb.RegisterTypeEncoder(tActorID, bsoncodec.ValueEncoderFunc(actorIDEncoder))

	return rb
}

func idEncoder(_ bsoncodec.EncodeContext, vw bsonrw.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tID {
		return bsoncodec.ValueEncoderError{Name: "idEncoder", Types: []reflect.Type{tID}, Received: val}
	}
	objectID, err := encodeID(val.Interface().(types.ID))
	if err != nil {
		return err
	}
	if err := vw.WriteObjectID(objectID); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}
	return nil
}

func actorIDEncoder(_ bsoncodec.EncodeContext, vw bsonrw.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tActorID {
		return bsoncodec.ValueEncoderError{Name: "actorIDEncoder", Types: []reflect.Type{tActorID}, Received: val}
	}
	objectID := encodeActorID(val.Interface().(*time.ActorID))
	if err := vw.WriteObjectID(objectID); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}
	return nil
}
