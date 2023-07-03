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

// InternalDocument represents a document in MongoDB and contains logical clocks.
type InternalDocument struct {
	key          key.Key
	status       StatusType
	root         *crdt.Root
	checkpoint   change.Checkpoint
	changeID     change.ID
	localChanges []*change.Change
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(k key.Key) *InternalDocument {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)

	return &InternalDocument{
		key:        k,
		status:     StatusDetached,
		root:       crdt.NewRoot(root),
		checkpoint: change.InitialCheckpoint,
		changeID:   change.InitialID,
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
		if _, err := d.GarbageCollect(pack.MinSyncedTicket); err != nil {
			return err
		}
	}

	return nil
}

// GarbageCollect purge elements that were removed before the given time.
func (d *InternalDocument) GarbageCollect(ticket *time.Ticket) (int, error) {
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
		d.changeID = d.changeID.SyncLamport(c.ID().Lamport())
	}

	return nil
}
