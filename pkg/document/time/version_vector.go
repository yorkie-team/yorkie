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
	"bytes"
	"math"
	"sort"
	"strconv"
	"strings"
)

// InitialVersionVector is the initial version vector.
var InitialVersionVector = VersionVector{}

// VersionVector is similar to vector clock, but it is synced with Lamport
// timestamp of the current change.
type VersionVector map[ActorID]int64

// NewVersionVector creates a new instance of VersionVector.
func NewVersionVector() VersionVector {
	return make(VersionVector)
}

// MinVersionVector returns the minimum version vector of the given version vectors.
// It computes the element-wise minimum across all input vectors.
// If any vector lacks a key, the minimum for that key is set to 0.
func MinVersionVector(vectors ...VersionVector) VersionVector {
	if len(vectors) == 0 {
		return InitialVersionVector
	}

	keySet := make(map[ActorID]struct{})
	for _, vec := range vectors {
		for k := range vec {
			keySet[k] = struct{}{}
		}
	}

	minVec := make(VersionVector)
	for k := range keySet {
		minValue := int64(math.MaxInt64)
		for _, vec := range vectors {
			if v, ok := vec[k]; ok {
				minValue = min(minValue, v)
			} else {
				minValue = 0
				break
			}
		}

		minVec[k] = minValue
	}

	return minVec
}

// Get gets the version of the given actor.
// Returns the version and whether the actor exists in the vector.
func (v VersionVector) Get(id ActorID) (int64, bool) {
	version, exists := v[id]
	return version, exists
}

// Set sets the given actor's version to the given value.
func (v VersionVector) Set(id ActorID, i int64) {
	v[id] = i
}

// Unset removes the version for the given actor from the VersionVector.
func (v VersionVector) Unset(id ActorID) {
	delete(v, id)
}

// VersionOf returns the version of the given actor.
func (v VersionVector) VersionOf(id ActorID) int64 {
	return v[id]
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

	keys := make([]ActorID, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return bytes.Compare(keys[i][:], keys[j][:]) < 0
	})

	isFirst := true
	for _, k := range keys {
		if !isFirst {
			builder.WriteRune(',')
		}

		id, err := ActorIDFromBytes(k[:])
		if err != nil {
			panic(err)
		}

		builder.WriteString(id.String())
		builder.WriteRune(':')
		builder.WriteString(strconv.FormatInt(v[k], 10))
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

// EqualToOrAfter returns whether this VersionVector's every field is equal or after than given ticket.
func (v VersionVector) EqualToOrAfter(other *Ticket) bool {
	clientLamport, ok := v[other.actorID]

	if !ok {
		return false
	}

	return clientLamport >= other.lamport
}

// Min modifies the receiver in-place to contain the minimum values between itself
// and the given version vector.
// Note: This method modifies the receiver for memory efficiency.
func (v VersionVector) Min(other *VersionVector) {
	for key, value := range v {
		if otherValue, exists := (*other)[key]; exists {
			v[key] = min(value, otherValue)
		} else {
			v[key] = 0
		}
	}

	for key := range *other {
		if _, exists := v[key]; !exists {
			v[key] = 0
		}
	}
}

// Max modifies the receiver in-place to contain the maximum values between itself
// and the given version vector.
// Note: This method modifies the receiver for memory efficiency.
func (v VersionVector) Max(other *VersionVector) {
	for key, value := range v {
		if otherValue, exists := (*other)[key]; exists {
			v[key] = max(value, otherValue)
		}
	}

	for key, value := range *other {
		if _, exists := v[key]; !exists {
			v[key] = value
		}
	}
}

// MaxLamport returns max lamport value in version vector.
func (v VersionVector) MaxLamport() int64 {
	var maxLamport int64 = -1

	for _, value := range v {
		maxLamport = max(maxLamport, value)
	}

	return maxLamport
}

// Filter returns filtered version vector which keys are only from filter
func (v VersionVector) Filter(filter []ActorID) VersionVector {
	filteredVV := NewVersionVector()

	for _, value := range filter {
		filteredVV[value] = v[value]
	}

	return filteredVV
}

// Keys returns a slice of ActorIDs present in the VersionVector.
func (v VersionVector) Keys() ([]ActorID, error) {
	var actors []ActorID

	for id := range v {
		actorID, err := ActorIDFromBytes(id[:])
		if err != nil {
			return nil, err
		}

		actors = append(actors, actorID)
	}

	return actors, nil
}
