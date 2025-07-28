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
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
)

const actorIDSize = 12

var (
	// InitialActorID represents the initial or server actor ID of the system.
	InitialActorID = ActorID{}

	// MaxActorID represents the maximum value of ActorID.
	MaxActorID = [actorIDSize]byte{
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
	}

	// ErrInvalidHexString is returned when the given string is not valid hex.
	ErrInvalidHexString = errors.New("invalid hex string")

	// ErrInvalidBase64String is returned when the given string is not valid base64.
	ErrInvalidBase64String = errors.New("invalid base64 string")

	// ErrInvalidActorID is returned when the given ID is not valid.
	ErrInvalidActorID = errors.New("invalid actor id")
)

// ActorID is a unique identifier for an actor in the system.
type ActorID [actorIDSize]byte

// ActorIDFromHex returns the bytes represented by the hexadecimal string str.
func ActorIDFromHex(str string) (ActorID, error) {
	if str == "" {
		return ActorID{}, fmt.Errorf("%s: %w", str, ErrInvalidHexString)
	}

	decoded, err := hex.DecodeString(str)
	if err != nil {
		return ActorID{}, fmt.Errorf("%s: %w", str, ErrInvalidHexString)
	}

	if len(decoded) != actorIDSize {
		return ActorID{}, fmt.Errorf("decoded length %d: %w", len(decoded), ErrInvalidHexString)
	}

	actorID := ActorID{}
	copy(actorID[:], decoded[:actorIDSize])

	return actorID, nil
}

// ActorIDFromBytes returns the bytes represented by the bytes of decoded hexadecimal string itself.
func ActorIDFromBytes(bytes []byte) (ActorID, error) {
	if len(bytes) == 0 {
		return ActorID{}, fmt.Errorf("bytes length %d: %w", len(bytes), ErrInvalidActorID)
	}

	if len(bytes) != actorIDSize {
		return ActorID{}, fmt.Errorf("bytes length %d: %w", len(bytes), ErrInvalidActorID)
	}

	return ActorID(bytes[:]), nil
}

// ActorIDFromBase64 returns the bytes represented by the base64 string str.
func ActorIDFromBase64(str string) (ActorID, error) {
	if str == "" {
		return ActorID{}, fmt.Errorf("%s: %w", str, ErrInvalidBase64String)
	}

	decoded, err := base64.RawStdEncoding.DecodeString(str)
	if err != nil {
		return ActorID{}, fmt.Errorf("%s: %w", str, ErrInvalidBase64String)
	}

	if len(decoded) != actorIDSize {
		return ActorID{}, ErrInvalidActorID
	}

	actorID := ActorID{}
	copy(actorID[:], decoded[:actorIDSize])

	return actorID, nil
}

// String returns the hexadecimal encoding of ActorID.
// If the receiver is nil, it would return empty string.
func (id ActorID) String() string {
	return hex.EncodeToString(id[:])
}

// StringBase64 returns the base64 encoding of ActorID.
func (id ActorID) StringBase64() string {
	return base64.RawStdEncoding.EncodeToString(id[:])
}

// Bytes returns the bytes of ActorID itself.
// If the receiver is nil, it would return empty array of byte.
func (id ActorID) Bytes() []byte {
	return id[:]
}

// Compare returns an integer comparing two ActorID lexicographically.
// The result will be 0 if id==other, -1 if id < other, and +1 if id > other.
// If the receiver or argument is nil, it would panic at runtime.
func (id ActorID) Compare(other ActorID) int {
	return bytes.Compare(id[:], other[:])
}
