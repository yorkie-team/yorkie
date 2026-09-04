/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package types

import (
	"crypto/sha256"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// stableActorTag is the permanent domain-separation tag for the stable actor
// derivation. Changing it changes every derived actor and orphans every
// persisted offline document, so bump the version only in an emergency.
const stableActorTag = "yorkie/stable-actor/v1"

// actorIDSize is the byte length of an actor ID (mirrors time.ActorID).
const actorIDSize = 12

// DeriveActorID deterministically derives a stable 12-byte actor ID from the
// given projectID and clientKey. The same inputs always yield the same actor,
// so two sessions of the same logical client resume the same actor without any
// coordination, unique index, or upsert.
//
// The derivation is SHA256(tag || projectID.Bytes() || clientKey), truncated to
// the first 12 bytes. projectID is placed first at its fixed 12-byte width so
// concatenation with the variable-length clientKey is unambiguous and the actor
// is namespaced per project. clientKey is used byte-for-byte, with no trimming
// or case normalization.
//
// The reserved actor values time.InitialActorID (all-zero) and time.MaxActorID
// (all-0xFF) are avoided by re-hashing until the result differs from both. The
// probability of hitting either is negligible; the loop only makes the fallback
// deterministic.
func DeriveActorID(projectID ID, clientKey string) ID {
	// projectID is an ObjectID hex string; use its 12 raw bytes so the
	// derivation matches the byte-level definition. It is always valid in
	// practice; on the impossible decode error, fall back to its raw bytes.
	projectBytes, err := projectID.Bytes()
	if err != nil {
		projectBytes = []byte(projectID)
	}

	h := sha256.New()
	h.Write([]byte(stableActorTag))
	h.Write(projectBytes)
	h.Write([]byte(clientKey))
	sum := h.Sum(nil)

	var actor [actorIDSize]byte
	copy(actor[:], sum[:actorIDSize])

	return resolveReserved(actor, sum)
}

// resolveReserved returns actor as an ID, re-hashing until it differs from the
// reserved values time.InitialActorID (all-zero) and time.MaxActorID (all-0xFF).
// seed is the SHA256 sum that produced actor and drives the deterministic
// re-hash. The probability of hitting a reserved value is ~2^-96; the loop only
// makes the fallback deterministic.
func resolveReserved(actor [actorIDSize]byte, seed []byte) ID {
	for actor == time.InitialActorID || actor == time.MaxActorID {
		next := sha256.New()
		next.Write([]byte(stableActorTag))
		next.Write([]byte{0x01})
		next.Write(seed)
		seed = next.Sum(nil)
		copy(actor[:], seed[:actorIDSize])
	}

	return IDFromBytes(actor[:])
}
