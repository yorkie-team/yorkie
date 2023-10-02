/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

package time

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const actorIDSize = 12

var (
	// InitialActorID represents the initial value of ActorID.
	InitialActorID = &ActorID{}

	// MaxActorID represents the maximum value of ActorID.
	MaxActorID = &ActorID{
		bytes: [actorIDSize]byte{
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
			math.MaxUint8,
		},
	}

	// ErrInvalidHexString is returned when the given string is not valid hex.
	ErrInvalidHexString = errors.New("invalid hex string")

	// ErrInvalidActorID is returned when the given ID is not valid.
	ErrInvalidActorID = errors.New("invalid actor id")
)

// ActorID represents the unique ID of the client. It is composed of 12 bytes.
// It caches the string representation of ActorID to reduce the number of calls
// to hex.EncodeToString. This causes a multi-routine problem, so it is
// recommended to use it in a single routine or to use it after locking.
type ActorID struct {
	bytes [actorIDSize]byte

	cachedString string
}

// ActorIDFromHex returns the bytes represented by the hexadecimal string str.
func ActorIDFromHex(str string) (*ActorID, error) {
	actorID := &ActorID{}

	if str == "" {
		return actorID, fmt.Errorf("%s: %w", str, ErrInvalidHexString)
	}

	decoded, err := hex.DecodeString(str)
	if err != nil {
		return actorID, fmt.Errorf("%s: %w", str, ErrInvalidHexString)
	}

	if len(decoded) != actorIDSize {
		return actorID, fmt.Errorf("decoded length %d: %w", len(decoded), ErrInvalidHexString)
	}

	copy(actorID.bytes[:], decoded[:actorIDSize])
	return actorID, nil
}

// ActorIDFromBytes returns the bytes represented by the bytes of decoded hexadecimal string itself.
func ActorIDFromBytes(bytes []byte) (*ActorID, error) {
	actorID := &ActorID{}

	if len(bytes) == 0 {
		return actorID, fmt.Errorf("bytes length %d: %w", len(bytes), ErrInvalidActorID)
	}

	if len(bytes) != actorIDSize {
		return actorID, fmt.Errorf("bytes length %d: %w", len(bytes), ErrInvalidActorID)
	}

	copy(actorID.bytes[:], bytes)
	return actorID, nil
}

// String returns the hexadecimal encoding of ActorID.
// If the receiver is nil, it would return empty string.
func (id *ActorID) String() string {
	if id.cachedString == "" {
		id.cachedString = hex.EncodeToString(id.bytes[:])
	}

	return id.cachedString
}

// Bytes returns the bytes of ActorID itself.
// If the receiver is nil, it would return empty array of byte.
func (id *ActorID) Bytes() []byte {
	return id.bytes[:]
}

// Compare returns an integer comparing two ActorID lexicographically.
// The result will be 0 if id==other, -1 if id < other, and +1 if id > other.
// If the receiver or argument is nil, it would panic at runtime.
func (id *ActorID) Compare(other *ActorID) int {
	return bytes.Compare(id.bytes[:], other.bytes[:])
}

// MarshalJSON ensures that when calling json.Marshal(),
// it is marshaled including private field.
func (id *ActorID) MarshalJSON() ([]byte, error) {
	result, err := json.Marshal(&struct{ Bytes [actorIDSize]byte }{
		Bytes: id.bytes,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal JSON: %w", err)
	}

	return result, nil
}

// UnmarshalJSON ensures that when calling json.Unmarshal(),
// it is unmarshalled including private field.
func (id *ActorID) UnmarshalJSON(bytes []byte) error {
	temp := &(struct{ Bytes [actorIDSize]byte }{
		Bytes: id.bytes,
	})
	if err := json.Unmarshal(bytes, temp); err != nil {
		return fmt.Errorf("unmarshal JSON: %w", err)
	}

	id.bytes = temp.Bytes
	return nil
}
