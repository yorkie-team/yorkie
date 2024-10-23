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
	"google.golang.org/protobuf/proto"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var tID = reflect.TypeOf(types.ID(""))
var tActorID = reflect.TypeOf(&time.ActorID{})
var tVersionVector = reflect.TypeOf(time.VersionVector{})

// NewRegistryBuilder returns a new registry builder with the default encoder and decoder.
func NewRegistryBuilder() *bsoncodec.RegistryBuilder {
	rb := bsoncodec.NewRegistryBuilder()

	bsoncodec.DefaultValueEncoders{}.RegisterDefaultEncoders(rb)
	bsoncodec.DefaultValueDecoders{}.RegisterDefaultDecoders(rb)
	bson.PrimitiveCodecs{}.RegisterPrimitiveCodecs(rb)

	// Register the decoders for types.ID.
	rb.RegisterCodec(
		tID,
		bsoncodec.NewStringCodec(bsonoptions.StringCodec().SetDecodeObjectIDAsHex(true)),
	)
	rb.RegisterTypeDecoder(tVersionVector, bsoncodec.ValueDecoderFunc(versionVectorDecoder))

	// Register the encoders for types.ID and time.ActorID.
	rb.RegisterTypeEncoder(tID, bsoncodec.ValueEncoderFunc(idEncoder))
	rb.RegisterTypeEncoder(tActorID, bsoncodec.ValueEncoderFunc(actorIDEncoder))
	rb.RegisterTypeEncoder(tVersionVector, bsoncodec.ValueEncoderFunc(versionVectorEncoder))

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

func versionVectorEncoder(_ bsoncodec.EncodeContext, vw bsonrw.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tVersionVector {
		return bsoncodec.ValueEncoderError{Name: "versionVectorEncoder", Types: []reflect.Type{tVersionVector}, Received: val}
	}

	pbChangeVector, err := converter.ToVersionVector(val.Interface().(time.VersionVector))
	if err != nil {
		return err
	}

	bytes, err := proto.Marshal(pbChangeVector)
	if err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	if err := vw.WriteBinary(bytes); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	return nil
}

func versionVectorDecoder(_ bsoncodec.DecodeContext, vr bsonrw.ValueReader, val reflect.Value) error {
	if val.Type() != tVersionVector {
		return bsoncodec.ValueDecoderError{Name: "versionVectorDecoder", Types: []reflect.Type{tVersionVector}, Received: val}
	}

	switch vrType := vr.Type(); vrType {
	case bson.TypeBinary:
		data, _, err := vr.ReadBinary()
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}

		var pbVector api.VersionVector
		if err := proto.Unmarshal(data, &pbVector); err != nil {
			return fmt.Errorf("decode error: %w", err)
		}

		vector, err := converter.FromVersionVector(&pbVector)
		if err != nil {
			return err
		}

		val.Set(reflect.ValueOf(vector))
	}

	return nil
}
