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

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/innerpresence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/server/backend/database"
)

var tID = reflect.TypeOf(types.ID(""))
var tActorID = reflect.TypeOf(time.ActorID{})
var tVersionVector = reflect.TypeOf(time.VersionVector{})
var tPresenceChange = reflect.TypeOf(&innerpresence.Change{})

// NewRegistryBuilder returns a new registry with the default encoder and decoder.
func NewRegistryBuilder() *bson.Registry {
	registry := bson.NewRegistry()

	// Register the decoders for types.ID.
	registry.RegisterTypeDecoder(tID, bson.ValueDecoderFunc(idDecoder))
	registry.RegisterTypeDecoder(tVersionVector, bson.ValueDecoderFunc(versionVectorDecoder))
	registry.RegisterTypeDecoder(tPresenceChange, bson.ValueDecoderFunc(presenceChangeDecoder))

	// Register the encoders for types.ID and time.ActorID.
	registry.RegisterTypeEncoder(tID, bson.ValueEncoderFunc(idEncoder))
	registry.RegisterTypeEncoder(tActorID, bson.ValueEncoderFunc(actorIDEncoder))
	registry.RegisterTypeEncoder(tVersionVector, bson.ValueEncoderFunc(versionVectorEncoder))
	registry.RegisterTypeEncoder(tPresenceChange, bson.ValueEncoderFunc(presenceChangeEncoder))

	return registry
}

func idDecoder(dc bson.DecodeContext, vr bson.ValueReader, val reflect.Value) error {
	if val.Type() != tID {
		return bson.ValueDecoderError{Name: "idDecoder", Types: []reflect.Type{tID}, Received: val}
	}

	switch vrType := vr.Type(); vrType {
	case bson.TypeObjectID:
		objectID, err := vr.ReadObjectID()
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		val.Set(reflect.ValueOf(types.ID(objectID.Hex())))
	case bson.TypeString:
		str, err := vr.ReadString()
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		val.Set(reflect.ValueOf(types.ID(str)))
	default:
		return fmt.Errorf("unsupported type: %v", vr.Type())
	}

	return nil
}

func idEncoder(_ bson.EncodeContext, vw bson.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tID {
		return bson.ValueEncoderError{Name: "idEncoder", Types: []reflect.Type{tID}, Received: val}
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

func actorIDEncoder(_ bson.EncodeContext, vw bson.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tActorID {
		return bson.ValueEncoderError{Name: "actorIDEncoder", Types: []reflect.Type{tActorID}, Received: val}
	}
	objectID := encodeActorID(val.Interface().(time.ActorID))
	if err := vw.WriteObjectID(objectID); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}
	return nil
}

func versionVectorEncoder(_ bson.EncodeContext, vw bson.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tVersionVector {
		return bson.ValueEncoderError{Name: "versionVectorEncoder", Types: []reflect.Type{tVersionVector}, Received: val}
	}

	vector := val.Interface().(time.VersionVector)
	bytes, err := vector.Bytes()
	if err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	if err := vw.WriteBinary(bytes); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	return nil
}

func versionVectorDecoder(_ bson.DecodeContext, vr bson.ValueReader, val reflect.Value) error {
	if val.Type() != tVersionVector {
		return bson.ValueDecoderError{Name: "versionVectorDecoder", Types: []reflect.Type{tVersionVector}, Received: val}
	}

	switch vrType := vr.Type(); vrType {
	case bson.TypeBinary:
		data, _, err := vr.ReadBinary()
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}

		vector, err := time.VersionVectorFromBytes(data)
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}

		val.Set(reflect.ValueOf(vector))
	default:
		return fmt.Errorf("unsupported type: %v", vr.Type())
	}

	return nil
}

func presenceChangeEncoder(_ bson.EncodeContext, vw bson.ValueWriter, val reflect.Value) error {
	if !val.IsValid() || val.Type() != tPresenceChange {
		return bson.ValueEncoderError{
			Name: "presenceChangeEncoder", Types: []reflect.Type{tPresenceChange}, Received: val}
	}

	presenceChange := val.Interface().(*innerpresence.Change)
	if presenceChange == nil {
		if err := vw.WriteNull(); err != nil {
			return fmt.Errorf("encode error: %w", err)
		}
		return nil
	}

	bytes, err := database.EncodePresenceChange(presenceChange)
	if err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	if err := vw.WriteBinary(bytes); err != nil {
		return fmt.Errorf("encode error: %w", err)
	}

	return nil
}

func presenceChangeDecoder(_ bson.DecodeContext, vr bson.ValueReader, val reflect.Value) error {
	if val.Type() != tPresenceChange {
		return bson.ValueDecoderError{
			Name: "presenceChangeDecoder", Types: []reflect.Type{tPresenceChange}, Received: val}
	}

	switch vrType := vr.Type(); vrType {
	case bson.TypeNull:
		if err := vr.ReadNull(); err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		val.Set(reflect.Zero(tPresenceChange))
		return nil
	case bson.TypeBinary:
		data, _, err := vr.ReadBinary()
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}

		presenceChange, err := database.PresenceChangeFromBytes(data)
		if err != nil {
			return fmt.Errorf("decode error: %w", err)
		}
		val.Set(reflect.ValueOf(presenceChange))
		return nil
	default:
		return fmt.Errorf("unsupported type: %v", vr.Type())
	}
}
