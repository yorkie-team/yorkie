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

const (
	// InitialLamport is the initial value of Lamport timestamp.
	InitialLamport = 0
)

var (
	// InitialID represents the initial state ID. Usually this is used to
	// represent a state where nothing has been edited.
	InitialID = NewID(InitialClientSeq, InitialServerSeq, InitialLamport, time.InitialActorID)
)

// ID is for identifying the Change. It is immutable.
type ID struct {
	// clientSeq is the sequence of the change within the client that made the
	// change.
	clientSeq uint32

	// serverSeq is the sequence of the change on the server. We can find the
	// change with serverSeq and documentID in the server. If the change is not
	// stored on the server, serverSeq is 0.
	serverSeq int64

	// lamport is lamport timestamp.
	lamport int64

	// actorID is actorID of this ID. If the actor is not set, it has initial
	// value.
	actorID *time.ActorID
}

// NewID creates a new instance of ID.
func NewID(
	clientSeq uint32,
	serverSeq int64,
	lamport int64,
	actorID *time.ActorID,
) ID {
	return ID{
		clientSeq: clientSeq,
		serverSeq: serverSeq,
		lamport:   lamport,
		actorID:   actorID,
	}
}

// Next creates a next ID of this ID.
func (id ID) Next() ID {
	return ID{
		clientSeq: id.clientSeq + 1,
		lamport:   id.lamport + 1,
		actorID:   id.actorID,
	}
}

// NewTimeTicket creates a ticket of the given delimiter.
func (id ID) NewTimeTicket(delimiter uint32) *time.Ticket {
	return time.NewTicket(
		id.lamport,
		delimiter,
		id.actorID,
	)
}

// SyncLamport syncs lamport timestamp with the given ID.
// https://en.wikipedia.org/wiki/Lamport_timestamps#Algorithm
func (id ID) SyncLamport(otherLamport int64) ID {
	if id.lamport < otherLamport {
		return NewID(id.clientSeq, InitialServerSeq, otherLamport, id.actorID)
	}

	return NewID(id.clientSeq, InitialServerSeq, id.lamport+1, id.actorID)
}

// SetActor sets actorID.
func (id ID) SetActor(actor *time.ActorID) ID {
	return NewID(id.clientSeq, InitialServerSeq, id.lamport, actor)
}

// SetServerSeq sets server sequence of this ID.
func (id ID) SetServerSeq(serverSeq int64) ID {
	return NewID(id.clientSeq, serverSeq, id.lamport, id.actorID)
}

// ClientSeq returns the client sequence of this ID.
func (id ID) ClientSeq() uint32 {
	return id.clientSeq
}

// ServerSeq returns the server sequence of this ID.
func (id ID) ServerSeq() int64 {
	return id.serverSeq
}

// Lamport returns the lamport clock of this ID.
func (id ID) Lamport() int64 {
	return id.lamport
}

// ActorID returns the actorID of this ID.
func (id ID) ActorID() *time.ActorID {
	return id.actorID
}
