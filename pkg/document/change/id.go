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

// ID represents the identifier of the change. It is used to identify the
// change and to order the changes. It is also used to detect the relationship
// between changes whether they are causally related or concurrent.
type ID struct {
	// clientSeq is the sequence of the change within the client that made the
	// change.
	clientSeq uint32

	// serverSeq is the sequence of the change on the server. We can find the
	// change with serverSeq and documentID in the server. If the change is not
	// stored on the server, serverSeq is 0.
	serverSeq int64

	// actorID is actorID of this ID. If the actor is not set, it has initial
	// value.
	actorID time.ActorID

	// lamport is lamport timestamp.
	lamport int64

	// versionVector is similar to vector clock, and it is used to detect the
	// relationship between changes whether they are causally related or concurrent.
	// It is synced with lamport timestamp of the change for mapping TimeTicket to
	// the change.
	versionVector time.VersionVector
}

// NewID creates a new instance of ID.
func NewID(
	clientSeq uint32,
	serverSeq int64,
	lamport int64,
	actorID time.ActorID,
	versionVector time.VersionVector,
) ID {
	return ID{
		clientSeq:     clientSeq,
		serverSeq:     serverSeq,
		lamport:       lamport,
		actorID:       actorID,
		versionVector: versionVector,
	}
}

// InitialID creates an initial state ID. Usually this is used to
// represent a state where nothing has been edited.
func InitialID() ID {
	return NewID(InitialClientSeq, InitialServerSeq, InitialLamport, time.InitialActorID, time.NewVersionVector())
}

// Next creates a next ID of this ID.
func (id ID) Next(excludeClocks ...bool) ID {
	if len(excludeClocks) > 0 && excludeClocks[0] {
		return ID{
			clientSeq: id.clientSeq + 1,
			actorID:   id.actorID,
		}
	}

	versionVector := id.versionVector.DeepCopy()
	versionVector.Set(id.actorID, id.lamport+1)

	return ID{
		clientSeq:     id.clientSeq + 1,
		lamport:       id.lamport + 1,
		actorID:       id.actorID,
		versionVector: versionVector,
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

// HasClocks returns whether this ID has clocks or not.
func (id ID) HasClocks() bool {
	return len(id.versionVector) > 0 && id.lamport != InitialLamport
}

// SyncClocks syncs logical clocks with the given ID. If the given ID
// doesn't have logical clocks, this ID is returned.
func (id ID) SyncClocks(other ID) ID {
	if !other.HasClocks() {
		return id
	}

	lamport := id.lamport + 1
	if id.lamport < other.lamport {
		lamport = other.lamport + 1
	}

	id.versionVector.Max(&other.versionVector)

	newID := NewID(id.clientSeq, InitialServerSeq, lamport, id.actorID, id.versionVector)
	newID.versionVector.Set(id.actorID, lamport)
	return newID
}

// SetClocks sets the given clocks to this ID. This is used when the snapshot is
// given from the server.
func (id ID) SetClocks(otherLamport int64, vector time.VersionVector) ID {
	lamport := id.lamport + 1
	if id.lamport < otherLamport {
		lamport = otherLamport + 1
	}

	// NOTE(chacha912): Documents created by server may have an InitialActorID
	// in their version vector. Although server is not an actual client, it
	// generates document snapshots from changes by participating with an
	// InitialActorID during document instance creation and accumulating stored
	// changes in DB.
	// Semantically, including a non-client actor in version vector is
	// problematic. To address this, we remove the InitialActorID from snapshots.
	vector.Unset(time.InitialActorID)
	id.versionVector.Max(&vector)

	newID := NewID(id.clientSeq, id.serverSeq, lamport, id.actorID, id.versionVector)
	newID.versionVector.Set(id.actorID, lamport)

	return newID
}

// SetVersionVector sets version vector
func (id ID) SetVersionVector(vector time.VersionVector) ID {
	return NewID(id.clientSeq, id.serverSeq, id.lamport, id.actorID, vector)
}

// SetActor sets actorID.
func (id ID) SetActor(actor time.ActorID) ID {
	// TODO(hackerwins): We need to update version vector as well.
	return NewID(id.clientSeq, InitialServerSeq, id.lamport, actor, id.versionVector)
}

// SetServerSeq sets server sequence of this ID.
func (id ID) SetServerSeq(serverSeq int64) ID {
	return NewID(id.clientSeq, serverSeq, id.lamport, id.actorID, id.versionVector)
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
func (id ID) ActorID() time.ActorID {
	return id.actorID
}

// VersionVector returns the version vector of this ID.
func (id ID) VersionVector() time.VersionVector {
	return id.versionVector
}

// AfterOrEqual returns whether this ID is causally after or equal the given ID.
func (id ID) AfterOrEqual(other ID) bool {
	return id.versionVector.AfterOrEqual(other.versionVector)
}
