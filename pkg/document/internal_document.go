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

package document

import (
	"errors"

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// StatusType represents the status of the document.
type StatusType int

const (
	// StatusDetached means that the document is not attached to the client.
	// The actor of the ticket is created without being assigned.
	StatusDetached StatusType = iota

	// StatusAttached means that this document is attached to the client.
	// The actor of the ticket is created with being assigned by the client.
	StatusAttached

	// StatusRemoved means that this document is removed. If the document is removed,
	// it cannot be edited.
	StatusRemoved
)

var (
	// ErrDocumentRemoved occurs when the document is removed.
	ErrDocumentRemoved = errors.New("document is removed")
)

// PeerChangedEvent represents events that occur when the states of another peers
// of the watched documents changes.
type PeerChangedEvent struct {
	Type      PeerChangedEventType
	Publisher map[string]presence.Presence
}

// PeerChangedEventType represents the type of PeerChangedEvent.
type PeerChangedEventType string

const (
	// WatchedEvent is an event that occurs when documents are watched by other clients.
	WatchedEvent PeerChangedEventType = "watched"

	// UnwatchedEvent is an event that occurs when documents are unwatched by other clients.
	UnwatchedEvent PeerChangedEventType = "unwatched"

	// PresenceChangedEvent is an event indicating that presence is changed.
	PresenceChangedEvent PeerChangedEventType = "presence-changed"
)

// InternalDocument represents a document in MongoDB and contains logical clocks.
type InternalDocument struct {
	key             key.Key
	status          StatusType
	root            *crdt.Root
	checkpoint      change.Checkpoint
	changeID        change.ID
	localChanges    []*change.Change
	myClientID      string
	peerPresenceMap map[string]presence.Info
	changeContext   *change.Context
	events          chan PeerChangedEvent
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(docKey key.Key, clientID string, initialPresence map[string]string) *InternalDocument {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	actorID, _ := time.ActorIDFromHex(clientID)
	peerPresenceMap := map[string]presence.Info{}
	copiedPresence := map[string]string{}
	for k, v := range initialPresence {
		copiedPresence[k] = v
	}
	peerPresenceMap[clientID] = presence.Info{
		Presence: copiedPresence,
	}
	return &InternalDocument{
		key:             docKey,
		status:          StatusDetached,
		root:            crdt.NewRoot(root),
		checkpoint:      change.InitialCheckpoint,
		changeID:        change.InitialIDWithActor(actorID),
		myClientID:      clientID,
		peerPresenceMap: peerPresenceMap,
		events:          make(chan PeerChangedEvent, 1),
	}
}

// NewInternalDocumentFromSnapshot creates a new instance of InternalDocument with the snapshot.
func NewInternalDocumentFromSnapshot(
	k key.Key,
	serverSeq int64,
	lamport int64,
	snapshot []byte,
) (*InternalDocument, error) {
	obj, err := converter.BytesToObject(snapshot)
	if err != nil {
		return nil, err
	}

	return &InternalDocument{
		key:        k,
		status:     StatusDetached,
		root:       crdt.NewRoot(obj),
		checkpoint: change.InitialCheckpoint.NextServerSeq(serverSeq),
		changeID:   change.InitialID.SyncLamport(lamport),
	}, nil
}

// Key returns the key of this document.
func (d *InternalDocument) Key() key.Key {
	return d.key
}

// Checkpoint returns the checkpoint of this document.
func (d *InternalDocument) Checkpoint() change.Checkpoint {
	return d.checkpoint
}

// HasLocalChanges returns whether this document has local changes or not.
func (d *InternalDocument) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

// ApplyChangePack applies the given change pack into this document.
func (d *InternalDocument) ApplyChangePack(pack *change.Pack) error {
	// 01. Apply remote changes to both the clone and the document.
	if len(pack.Snapshot) > 0 {
		if err := d.applySnapshot(pack.Snapshot, pack.Checkpoint.ServerSeq); err != nil {
			return err
		}
	} else {
		if err := d.ApplyChanges(pack.Changes...); err != nil {
			return err
		}
	}

	// 02. Remove local changes applied to server.
	for d.HasLocalChanges() {
		c := d.localChanges[0]
		if c.ClientSeq() > pack.Checkpoint.ClientSeq {
			break
		}
		d.localChanges = d.localChanges[1:]
	}

	// 03. Update the checkpoint.
	d.checkpoint = d.checkpoint.Forward(pack.Checkpoint)

	if pack.MinSyncedTicket != nil {
		d.GarbageCollect(pack.MinSyncedTicket)
	}

	return nil
}

// GarbageCollect purge elements that were removed before the given time.
func (d *InternalDocument) GarbageCollect(ticket *time.Ticket) int {
	return d.root.GarbageCollect(ticket)
}

// GarbageLen returns the count of removed elements.
func (d *InternalDocument) GarbageLen() int {
	return d.root.GarbageLen()
}

// Marshal returns the JSON encoding of this document.
func (d *InternalDocument) Marshal() string {
	return d.root.Object().Marshal()
}

// CreateChangePack creates pack of the local changes to send to the server.
func (d *InternalDocument) CreateChangePack() *change.Pack {
	changes := d.localChanges

	cp := d.checkpoint.IncreaseClientSeq(uint32(len(changes)))
	return change.NewPack(d.key, cp, changes, nil)
}

// SetActor sets actor into this document. This is also applied in the local
// changes the document has.
func (d *InternalDocument) SetActor(actor *time.ActorID) {
	for _, c := range d.localChanges {
		c.SetActor(actor)
	}
	d.changeID = d.changeID.SetActor(actor)
}

// Lamport returns the Lamport clock of this document.
func (d *InternalDocument) Lamport() int64 {
	return d.changeID.Lamport()
}

// ActorID returns ID of the actor currently editing the document.
func (d *InternalDocument) ActorID() *time.ActorID {
	return d.changeID.ActorID()
}

// SetStatus sets the status of this document.
func (d *InternalDocument) SetStatus(status StatusType) {
	d.status = status
}

// IsAttached returns the whether this document is attached or not.
func (d *InternalDocument) IsAttached() bool {
	return d.status == StatusAttached
}

// SetPresenceInfo sets the presence information of the given client.
func (d *InternalDocument) SetPresenceInfo(clientID string, info presence.Info) {
	if _, ok := d.peerPresenceMap[clientID]; !ok {
		d.peerPresenceMap[clientID] = info
	} else {
		presenceInfo := d.peerPresenceMap[clientID]
		ok := presenceInfo.Update(info)
		if ok {
			d.peerPresenceMap[clientID] = presenceInfo
		}
	}
}

// PresenceInfo returns the presence information of the given client.
func (d *InternalDocument) PresenceInfo(clientID string) presence.Info {
	return d.peerPresenceMap[clientID]
}

// RemovePresenceInfo removes the presence information of the given client.
func (d *InternalDocument) RemovePresenceInfo(clientID string) {
	delete(d.peerPresenceMap, clientID)
}

// Presence returns the presence of the client who created this document.
func (d *InternalDocument) Presence() presence.Presence {
	presence := make(presence.Presence)
	for k, v := range d.peerPresenceMap[d.myClientID].Presence {
		presence[k] = v
	}
	return presence
}

// PeerPresence returns the presence of the given client.
func (d *InternalDocument) PeerPresence(clientID string) presence.Presence {
	presence := make(presence.Presence)
	for k, v := range d.peerPresenceMap[clientID].Presence {
		presence[k] = v
	}
	return presence
}

// PeersMap returns the list of peers, including the client who created this document.
func (d *InternalDocument) PeersMap() map[string]presence.Presence {
	peers := map[string]presence.Presence{}

	for c, p := range d.peerPresenceMap {
		peers[c] = map[string]string{}
		for k, v := range p.Presence {
			peers[c][k] = v
		}
	}
	return peers
}

// Root returns the root of this document.
func (d *InternalDocument) Root() *crdt.Root {
	return d.root
}

// RootObject returns the root object.
func (d *InternalDocument) RootObject() *crdt.Object {
	return d.root.Object()
}

func (d *InternalDocument) applySnapshot(snapshot []byte, serverSeq int64) error {
	rootObj, err := converter.BytesToObject(snapshot)
	if err != nil {
		return err
	}

	d.root = crdt.NewRoot(rootObj)
	d.changeID = d.changeID.SyncLamport(serverSeq)

	return nil
}

// ApplyChanges applies remote changes to the document.
func (d *InternalDocument) ApplyChanges(changes ...*change.Change) error {
	for _, c := range changes {
		if err := c.Execute(d.root); err != nil {
			return err
		}
		if c.PresenceInfo() != nil && d.peerPresenceMap != nil {
			d.SetPresenceInfo(c.ID().ActorID().String(), *c.PresenceInfo())
			if c.ID().ActorID().String() != d.myClientID {
				d.events <- PeerChangedEvent{
					Type: PresenceChangedEvent,
					Publisher: map[string]presence.Presence{
						c.ID().ActorID().String(): d.PeerPresence(c.ID().ActorID().String()),
					},
				}
			}
		}

		d.changeID = d.changeID.SyncLamport(c.ID().Lamport())
	}

	return nil
}

// Events returns the PeerChangedEvent channel of this document.
func (d *InternalDocument) Events() chan PeerChangedEvent {
	return d.events
}
