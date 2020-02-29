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
)

var (
	InitialActorID = &ActorID{}
	MaxActorID     = &ActorID{
		255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255, 255,
	}
)

type ActorID [12]byte

func ActorIDFromHex(str string) *ActorID {
	if str == "" {
		return nil
	}

	actorID := ActorID{}
	decoded, err := hex.DecodeString(str)
	if err != nil {
		panic("fail to decode hex")
	}
	copy(actorID[:], decoded[:12])
	return &actorID
}

func (id *ActorID) String() string {
	if id == nil {
		return ""
	}

	return hex.EncodeToString(id[:])
}

func (id *ActorID) Compare(other *ActorID) int {
	if id == nil || other == nil {
		panic("actorID cannot be null")
	}

	return bytes.Compare(id[:], other[:])
}
