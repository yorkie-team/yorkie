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
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// InitialID represents the initial state ID. Usually this is used to
	// represent a state where nothing has been edited.
	InitialID = NewID(0, 0, time.InitialActorID)
)

// ID is for identifying the Change. This struct is immutable.
type ID struct {
	// clientSeq is a sequence index of the change on this client.
	clientSeq uint32
	// lamport is lamport timestamp.
	lamport uint64
	// actor is an ID of actor.
	actor *time.ActorID
}

// NewID creates a new instance of ID.
func NewID(
	clientSeq uint32,
	lamport uint64,
	actorID *time.ActorID,
) *ID {
	id := &ID{
		clientSeq: clientSeq,
		lamport:   lamport,
		actor:     actorID,
	}

	return id
}

// Next creates a next ID of this ID.
func (id *ID) Next() *ID {
	return &ID{
		clientSeq: id.clientSeq + 1,
		lamport:   id.lamport + 1,
		actor:     id.actor,
	}
}

// NewTimeTicket creates a ticket of the given delimiter.
func (id *ID) NewTimeTicket(delimiter uint32) *time.Ticket {
	return time.NewTicket(
		id.lamport,
		delimiter,
		id.actor,
	)
}

func (id *ID) Sync(other *ID) *ID {
	if id.lamport < other.lamport {
		return NewID(id.clientSeq, other.lamport, id.actor)
	}

	return NewID(id.clientSeq, id.lamport+1, id.actor)
}

// SetActor sets actor.
func (id *ID) SetActor(actor *time.ActorID) *ID {
	return NewID(id.clientSeq, id.lamport, actor)
}

// ClientSeq returns the client sequence of this ID.
func (id *ID) ClientSeq() uint32 {
	return id.clientSeq
}

// Lamport returns the lamport clock of this ID.
func (id *ID) Lamport() uint64 {
	return id.lamport
}

// Actor returns the actor of this ID.
func (id *ID) Actor() *time.ActorID {
	return id.actor
}
