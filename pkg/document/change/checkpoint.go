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

package change

import (
	"fmt"
	"math"
)

const (
	// InitialClientSeq is the initial sequence number of the client.
	InitialClientSeq = 0

	// InitialServerSeq is the initial sequence number of the server.
	InitialServerSeq = 0

	// MaxClientSeq is the maximum sequence number of the client.
	MaxClientSeq = math.MaxUint32

	// MaxServerSeq is the maximum sequence number of the server.
	MaxServerSeq = int64(math.MaxInt64)
)

// InitialCheckpoint is the initial value of Checkpoint.
var InitialCheckpoint = NewCheckpoint(NewServerSeq(InitialServerSeq, InitialServerSeq), InitialClientSeq)

// MaxCheckpoint is the maximum value of Checkpoint.
var MaxCheckpoint = NewCheckpoint(NewServerSeq(MaxServerSeq, MaxServerSeq), MaxClientSeq)

// Checkpoint is used to determine the client received changes.
// It is not meant to be used to determine the logical order of changes.
type Checkpoint struct {
	// serverSeq contains operation and presence sequence numbers on the server.
	// We can find the change with serverSeq and documentID in the server.
	// If the change is not stored on the server, both sequences are 0.
	ServerSeq ServerSeq

	// clientSeq is the sequence of the change within the client that made the
	// change.
	ClientSeq uint32
}

// NewCheckpoint creates a new instance of Checkpoint.
func NewCheckpoint(serverSeq ServerSeq, clientSeq uint32) Checkpoint {
	return Checkpoint{
		ServerSeq: serverSeq,
		ClientSeq: clientSeq,
	}
}

// NextServerSeq creates a new instance with next server sequence.
func (cp Checkpoint) NextServerSeq(serverSeq ServerSeq) Checkpoint {
	if cp.ServerSeq.OpSeq == serverSeq.OpSeq && cp.ServerSeq.PrSeq == serverSeq.PrSeq {
		return cp
	}

	return NewCheckpoint(serverSeq, cp.ClientSeq)
}

// NextOpSeq creates a new instance with next operation sequence.
func (cp Checkpoint) NextOpSeq(opSeq int64) Checkpoint {
	newServerSeq := NewServerSeq(opSeq, cp.ServerSeq.PrSeq)
	return NewCheckpoint(newServerSeq, cp.ClientSeq)
}

// NextPrSeq creates a new instance with next presence sequence.
func (cp Checkpoint) NextPrSeq(prSeq int64) Checkpoint {
	newServerSeq := NewServerSeq(cp.ServerSeq.OpSeq, prSeq)
	return NewCheckpoint(newServerSeq, cp.ClientSeq)
}

// NextClientSeq creates a new instance with next client sequence.
func (cp Checkpoint) NextClientSeq() Checkpoint {
	return cp.IncreaseClientSeq(1)
}

// IncreaseClientSeq creates a new instance with increased client sequence.
func (cp Checkpoint) IncreaseClientSeq(inc uint32) Checkpoint {
	if inc == 0 {
		return cp
	}
	return NewCheckpoint(cp.ServerSeq, cp.ClientSeq+inc)
}

// SyncClientSeq updates the given clientSeq if it is greater than the internal
// value.
func (cp Checkpoint) SyncClientSeq(clientSeq uint32) Checkpoint {
	if cp.ClientSeq < clientSeq {
		return NewCheckpoint(cp.ServerSeq, clientSeq)
	}

	return cp
}

// Forward updates the given checkpoint with those values when it is greater
// than the values of internal properties.
func (cp Checkpoint) Forward(other Checkpoint) Checkpoint {
	if cp.Equal(other) {
		return cp
	}

	maxOpSeq := max(cp.ServerSeq.OpSeq, other.ServerSeq.OpSeq)
	maxPrSeq := max(cp.ServerSeq.PrSeq, other.ServerSeq.PrSeq)
	maxServerSeq := NewServerSeq(maxOpSeq, maxPrSeq)

	maxClientSeq := max(cp.ClientSeq, other.ClientSeq)

	return NewCheckpoint(maxServerSeq, maxClientSeq)
}

// Equal returns whether the given checkpoint is equal to this checkpoint or not.
func (cp Checkpoint) Equal(other Checkpoint) bool {
	return cp.ServerSeq.OpSeq == other.ServerSeq.OpSeq &&
		cp.ServerSeq.PrSeq == other.ServerSeq.PrSeq &&
		cp.ClientSeq == other.ClientSeq
}

// String returns the string of information about this checkpoint.
func (cp Checkpoint) String() string {
	return fmt.Sprintf("opSeq=%d, prSeq=%d, clientSeq=%d", cp.ServerSeq.OpSeq, cp.ServerSeq.PrSeq, cp.ClientSeq)
}


