/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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
	"strconv"
	"strings"
)

// InitialVersionVector is the initial version vector.
var InitialVersionVector = VersionVector{}

// VersionVector is similar to vector clock, but it is synced with lamport
// timestamp of the current change.
type VersionVector map[actorID]int64

// NewVersionVector creates a new instance of VersionVector.
func NewVersionVector() VersionVector {
	return make(VersionVector)
}

// Set sets the given actor's version to the given value.
func (v VersionVector) Set(id *ActorID, i int64) {
	v[id.bytes] = i
}

// VersionOf returns the version of the given actor.
func (v VersionVector) VersionOf(id *ActorID) int64 {
	return v[id.bytes]
}

// DeepCopy creates a deep copy of this VersionVector.
func (v VersionVector) DeepCopy() VersionVector {
	copied := NewVersionVector()
	for k, v := range v {
		copied[k] = v
	}
	return copied
}

// Marshal returns the JSON encoding of this VersionVector.
func (v VersionVector) Marshal() string {
	builder := strings.Builder{}

	builder.WriteRune('{')

	isFirst := true
	for k, val := range v {
		if !isFirst {
			builder.WriteRune(',')
		}

		id, err := ActorIDFromBytes(k[:])
		if err != nil {
			panic(err)
		}

		builder.WriteString(id.String())
		builder.WriteRune(':')
		builder.WriteString(strconv.FormatInt(val, 10))
		isFirst = false
	}
	builder.WriteRune('}')

	return builder.String()
}

// AfterOrEqual returns whether this VersionVector is causally after or equal
// the given VersionVector.
func (v VersionVector) AfterOrEqual(other VersionVector) bool {
	for k, val := range v {
		if val < other[k] {
			return false
		}
	}

	for k, val := range other {
		if v[k] < val {
			return false
		}
	}

	return true
}

// After returns whether this VersionVector is causally after the given ticket.
func (v VersionVector) After(other *Ticket) bool {
	actorID := other.ActorID()
	ticket := NewTicket(v.VersionOf(actorID), MaxDelimiter, actorID)
	return ticket.Compare(other) >= 0
}
