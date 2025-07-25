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

// Package time provides logical clock and ticket for document.
package time

import (
	"fmt"
	"math"
	"strconv"
)

const (
	// InitialLamport is the initial value of Lamport timestamp.
	InitialLamport = 0

	// MaxLamport is the maximum value stored in lamport.
	MaxLamport = math.MaxInt64

	// MaxDelimiter is the maximum value stored in delimiter.
	MaxDelimiter = math.MaxUint32

	// TicketSize is the size of Ticket in bytes.
	// lamport(int64) + delimiter(uint32) + actorID(12 bytes)
	TicketSize = 8 + 4 + 12
)

var (
	// InitialTicket is the initial value of Ticket.
	InitialTicket = NewTicket(
		0,
		0,
		InitialActorID,
	)

	// MaxTicket is the maximum value of Ticket.
	MaxTicket = NewTicket(
		MaxLamport,
		MaxDelimiter,
		MaxActorID,
	)
)

// Ticket represents the logical clock. It is used to determine the order of
// changes and identify elements and nodes in the document. It can't be used
// to detect the relationship between changes whether they are causally related
// or concurrent.
type Ticket struct {
	lamport   int64
	delimiter uint32
	actorID   ActorID

	// cachedKey is the cache of the string representation of the ticket.
	cachedKey string

	// cachedActorIDBase64 is the cache of the base64 representation of the actorID.
	cachedActorIDBase64 string
}

// NewTicket creates an instance of Ticket.
func NewTicket(
	lamport int64,
	delimiter uint32,
	actorID ActorID,
) *Ticket {
	return &Ticket{
		lamport:   lamport,
		delimiter: delimiter,
		actorID:   actorID,
	}
}

// ToTestString returns a string containing the metadata of the ticket
// for debugging purpose.
func (t *Ticket) ToTestString() string {
	return fmt.Sprintf(
		"%d:%d:%s", t.lamport, t.delimiter, t.actorID.StringBase64()[14:16],
	)
}

// Key returns the key string for this Ticket.
func (t *Ticket) Key() string {
	if t.cachedKey == "" {
		t.cachedKey = strconv.FormatInt(t.lamport, 10) +
			":" +
			strconv.FormatInt(int64(t.delimiter), 10) +
			":" +
			t.actorID.StringBase64()

	}

	return t.cachedKey
}

// Lamport returns the lamport value.
func (t *Ticket) Lamport() int64 {
	return t.lamport
}

// Delimiter returns the delimiter value.
func (t *Ticket) Delimiter() uint32 {
	return t.delimiter
}

// ActorID returns the actorID value.
func (t *Ticket) ActorID() ActorID {
	return t.actorID
}

// ActorIDBase64 returns the actorID's base64 value.
func (t *Ticket) ActorIDBase64() string {
	if t.cachedActorIDBase64 == "" {
		t.cachedActorIDBase64 = t.actorID.StringBase64()
	}
	return t.cachedActorIDBase64
}

// ActorIDBytes returns the actorID's bytes value.
func (t *Ticket) ActorIDBytes() []byte {
	return t.actorID.Bytes()
}

// After returns whether the given ticket was created later.
func (t *Ticket) After(other *Ticket) bool {
	return t.Compare(other) > 0
}

// Compare returns an integer comparing two Ticket.
// The result will be 0 if id==other, -1 if id < other, and +1 if id > other.
// If the receiver or argument is nil, it would panic at runtime.
func (t *Ticket) Compare(other *Ticket) int {
	if t.lamport > other.lamport {
		return 1
	} else if t.lamport < other.lamport {
		return -1
	}

	compare := t.actorID.Compare(other.ActorID())
	if compare != 0 {
		return compare
	}

	if t.delimiter > other.delimiter {
		return 1
	} else if t.delimiter < other.delimiter {
		return -1
	}

	return 0
}

// SetActorID creates a new instance of Ticket with the given actorID.
func (t *Ticket) SetActorID(actorID ActorID) *Ticket {
	return &Ticket{
		lamport:   t.lamport,
		delimiter: t.delimiter,
		actorID:   actorID,
	}
}
