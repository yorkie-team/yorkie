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
	"fmt"
	"math"
)

var (
	InitialTicket = NewTicket(
		0,
		0,
		InitialActorID,
	)
	MaxTicket = NewTicket(
		math.MaxUint64,
		math.MaxUint32,
		MaxActorID,
	)
)

// Ticket is a timestamp of the logical clock. Ticket is immutable.
// It is created by change.ID.
type Ticket struct {
	lamport   uint64
	delimiter uint32
	actorID   *ActorID
}

func NewTicket(
	lamport uint64,
	delimiter uint32,
	actorID *ActorID,
) *Ticket {
	return &Ticket{
		lamport:   lamport,
		delimiter: delimiter,
		actorID:   actorID,
	}
}

// AnnotatedString returns a string containing the meta data of the ticket
// for debugging purpose.
func (t *Ticket) AnnotatedString() string {
	if t.actorID == nil {
		return fmt.Sprintf("%d:%d:nil", t.lamport, t.delimiter)
	}

	return fmt.Sprintf(
		"%d:%d:%s", t.lamport, t.delimiter, t.actorID.String()[22:24],
	)
}

func (t *Ticket) Key() string {
	if t.actorID == nil {
		return fmt.Sprintf("%d:%d:", t.lamport, t.delimiter)
	}

	return fmt.Sprintf(
		"%d:%d:%s", t.lamport, t.delimiter, t.actorID.String(),
	)
}

func (t *Ticket) Lamport() uint64 {
	return t.lamport
}

func (t *Ticket) Delimiter() uint32 {
	return t.delimiter
}

func (t *Ticket) ActorID() *ActorID {
	return t.actorID
}

func (t *Ticket) ActorIDHex() string {
	return t.actorID.String()
}

func (t *Ticket) After(other *Ticket) bool {
	return t.Compare(other) > 0
}

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

func (t *Ticket) SetActorID(actorID *ActorID) *Ticket {
	return &Ticket{
		lamport:   t.lamport,
		delimiter: t.delimiter,
		actorID:   actorID,
	}
}
