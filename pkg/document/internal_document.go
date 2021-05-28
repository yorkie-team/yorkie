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
	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/internal/log"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

type statusType int

const (
	// Detached means that the document is not attached to the client.
	// The actor of the ticket is created without being assigned.
	Detached statusType = iota

	// Attached means that this document is attached to the client.
	// The actor of the ticket is created with being assigned by the client.
	Attached
)

// InternalDocument represents a document in MongoDB and contains logical clocks.
type InternalDocument struct {
	key          *key.Key
	status       statusType
	root         *json.Root
	checkpoint   *checkpoint.Checkpoint
	changeID     *change.ID
	localChanges []*change.Change
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(collection, document string) *InternalDocument {
	root := json.NewObject(json.NewRHTPriorityQueueMap(), time.InitialTicket)

	return &InternalDocument{
		key:        &key.Key{Collection: collection, Document: document},
		status:     Detached,
		root:       json.NewRoot(root),
		checkpoint: checkpoint.Initial,
		changeID:   change.InitialID,
	}
}

// NewInternalDocumentFromSnapshot creates a new instance of InternalDocument with the snapshot.
func NewInternalDocumentFromSnapshot(
	collection string,
	document string,
	serverSeq uint64,
	snapshot []byte,
) (*InternalDocument, error) {
	obj, err := converter.BytesToObject(snapshot)
	if err != nil {
		return nil, err
	}

	return &InternalDocument{
		key:        &key.Key{Collection: collection, Document: document},
		status:     Detached,
		root:       json.NewRoot(obj),
		checkpoint: checkpoint.Initial.NextServerSeq(serverSeq),
		changeID:   change.InitialID,
	}, nil
}

// Key returns the key of this document.
func (d *InternalDocument) Key() *key.Key {
	return d.key
}

// Checkpoint returns the checkpoint of this document.
func (d *InternalDocument) Checkpoint() *checkpoint.Checkpoint {
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
		if err := d.applyChanges(pack.Changes); err != nil {
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

	log.Logger.Debugf("after apply %d changes: %s", len(pack.Changes), d.RootObject().Marshal())
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

// Actor sets actor.
func (d *InternalDocument) Actor() *time.ActorID {
	return d.changeID.Actor()
}

// SetStatus sets the status of this document.
func (d *InternalDocument) SetStatus(status statusType) {
	d.status = status
}

// IsAttached returns the whether this document is attached or not.
func (d *InternalDocument) IsAttached() bool {
	return d.status == Attached
}

// RootObject returns the root object.
func (d *InternalDocument) RootObject() *json.Object {
	return d.root.Object()
}

func (d *InternalDocument) applySnapshot(snapshot []byte, serverSeq uint64) error {
	rootObj, err := converter.BytesToObject(snapshot)
	if err != nil {
		return err
	}

	d.root = json.NewRoot(rootObj)

	if d.HasLocalChanges() {
		for _, c := range d.localChanges {
			if err := c.Execute(d.root); err != nil {
				return err
			}
		}
	}
	d.changeID = d.changeID.SyncLamport(serverSeq)

	return nil
}

// applyChanges applies remote changes to both the clone and the document.
func (d *InternalDocument) applyChanges(changes []*change.Change) error {
	for _, c := range changes {
		if err := c.Execute(d.root); err != nil {
			return err
		}
		d.changeID = d.changeID.SyncLamport(c.ID().Lamport())
	}

	return nil
}
