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

// Package types provides the types used in the Yorkie API. This package is
// used by both the server and the client.
package types

import (
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrInvalidID is returned when the given ID is not ObjectID.
	ErrInvalidID = errors.New("invalid ID")
)

// ID represents ID of entity.
type ID string

// String returns a string representation of this ID.
func (id *ID) String() string {
	return string(*id)
}

// Bytes returns bytes of decoded hexadecimal string representation of this ID.
func (id *ID) Bytes() ([]byte, error) {
	decoded, err := hex.DecodeString(id.String())
	if err != nil {
		return nil, fmt.Errorf("decode hex string: %w", err)
	}
	return decoded, nil
}

// Validate returns error if this ID is invalid.
func (id *ID) Validate() error {
	b, err := hex.DecodeString(id.String())
	if err != nil {
		return fmt.Errorf("%s: %w", id, ErrInvalidID)
	}

	if len(b) != 12 {
		return fmt.Errorf("%s: %w", id, ErrInvalidID)
	}

	return nil
}

// ToActorID returns ActorID from this ID.
func (id *ID) ToActorID() (*time.ActorID, error) {
	b, err := id.Bytes()
	if err != nil {
		return &time.ActorID{}, err
	}

	return time.ActorIDFromBytes(b)
}

// IDFromBytes returns ID represented by the encoded hexadecimal string from bytes.
func IDFromBytes(bytes []byte) ID {
	return ID(hex.EncodeToString(bytes))
}

// IDFromActorID returns ID represented by the encoded hexadecimal string from actor ID.
func IDFromActorID(actorID *time.ActorID) ID {
	return IDFromBytes(actorID.Bytes())
}
