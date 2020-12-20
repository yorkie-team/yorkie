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

package checkpoint

import (
	"fmt"
)

// Initial is the initial value of the checkpoint.
var Initial = New(0, 0)

// Checkpoint is used to determine the client received changes.
type Checkpoint struct {
	ServerSeq uint64
	ClientSeq uint32
}

// New creates a new instance of Checkpoint.
func New(serverSeq uint64, clientSeq uint32) *Checkpoint {
	return &Checkpoint{
		ServerSeq: serverSeq,
		ClientSeq: clientSeq,
	}
}

// NextClientSeq creates a new instance with next client sequence.
func (cp *Checkpoint) NextClientSeq() *Checkpoint {
	return cp.IncreaseClientSeq(1)
}

// NextServerSeq creates a new instance with next server sequence.
func (cp *Checkpoint) NextServerSeq(serverSeq uint64) *Checkpoint {
	if cp.ServerSeq == serverSeq {
		return cp
	}

	return New(serverSeq, cp.ClientSeq)
}

// IncreaseClientSeq creates a new instance with increased client sequence.
func (cp *Checkpoint) IncreaseClientSeq(inc uint32) *Checkpoint {
	if inc == 0 {
		return cp
	}
	return New(cp.ServerSeq, cp.ClientSeq+inc)
}

// SyncClientSeq updates the given clientSeq if it is greater than the internal
// value.
func (cp *Checkpoint) SyncClientSeq(clientSeq uint32) *Checkpoint {
	if cp.ClientSeq < clientSeq {
		return New(cp.ServerSeq, clientSeq)
	}

	return cp
}

// Forward updates the given checkpoint with those values when it is greater
// than the values of internal properties.
func (cp *Checkpoint) Forward(other *Checkpoint) *Checkpoint {
	if cp.Equals(other) {
		return cp
	}

	maxServerSeq := cp.ServerSeq
	if cp.ServerSeq < other.ServerSeq {
		maxServerSeq = other.ServerSeq
	}

	maxClientSeq := cp.ClientSeq
	if cp.ClientSeq < other.ClientSeq {
		maxClientSeq = other.ClientSeq
	}

	return New(maxServerSeq, maxClientSeq)
}

// Equals returns whether the given checkpoint is equal to this checkpoint or not.
func (cp *Checkpoint) Equals(other *Checkpoint) bool {
	return cp.ServerSeq == other.ServerSeq &&
		cp.ClientSeq == other.ClientSeq
}

// String returns the string of information about this checkpoint.
func (cp *Checkpoint) String() string {
	return fmt.Sprintf("serverSeq=%d, clientSeq=%d", cp.ServerSeq, cp.ClientSeq)
}
