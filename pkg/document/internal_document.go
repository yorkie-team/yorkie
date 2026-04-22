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
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/key"
)

type StatusType = attachable.StatusType

const (
	StatusDetached = attachable.StatusDetached
	StatusAttached = attachable.StatusAttached
	StatusRemoved  = attachable.StatusRemoved
)

var (
	// ErrDocumentRemoved occurs when the document is removed.
	ErrDocumentRemoved = errors.FailedPrecond("document is removed")
)

// InternalDocument is a document that is used internally. It is not directly
// exposed to the user.
type InternalDocument struct {
	// key is the key of the document. It is used as the key of the document in
	// user's perspective.
	key key.Key

	// status is the status of the document. It is used to check whether the
	// document is attached to the client or detached or removed.
	status StatusType

	// checkpoint is the checkpoint of the document. It is used to determine
	// what changes should be sent and what changes should be received.
	checkpoint change.Checkpoint

	// changeID is the ID of the last change. It is used to create a new change.
	// It contains logical clock information like the lamport timestamp, actorID
	// and checkpoint information.
	changeID change.ID

	// root is the root of the document. It is used to store JSON-like data in
	// CRDT manner.
	root *crdt.Root

	// presences is the map of the presence. It is used to store the presence
	// of the actors who are attaching this document.
	presences *presence.Map

	// onlineClients is the set of the client who is editing this document in
	// online.
	onlineClients map[string]bool

	// localChanges is the list of the changes that are not yet sent to the
	// server.
	localChanges []*change.Change

	// detachedActors is a map of detached actor IDs to their lamport at detach time.
	// Used to augment VV for correct causality detection and GC after VV cleanup.
	detachedActors map[time.ActorID]int64
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(k key.Key) *InternalDocument {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)

	// TODO(hackerwins): We need to initialize the presence of the actor who edited the document.
	return &InternalDocument{
		key:            k,
		status:         StatusDetached,
		root:           crdt.NewRoot(root),
		checkpoint:     change.InitialCheckpoint,
		changeID:       change.InitialID(),
		presences:      presence.NewMap(),
		onlineClients:  make(map[string]bool),
		detachedActors: make(map[time.ActorID]int64),
	}
}

// NewInternalDocumentFromSnapshot creates a new instance of InternalDocument with the snapshot.
func NewInternalDocumentFromSnapshot(
	k key.Key,
	serverSeq int64,
	lamport int64,
	vector time.VersionVector,
	snapshot []byte,
) (*InternalDocument, error) {
	obj, presences, detachedActors, err := converter.BytesToSnapshot(snapshot)
	if err != nil {
		return nil, err
	}

	if detachedActors == nil {
		detachedActors = make(map[time.ActorID]int64)
	}

	return &InternalDocument{
		key:            k,
		status:         StatusDetached,
		root:           crdt.NewRoot(obj),
		presences:      presences,
		onlineClients:  make(map[string]bool),
		checkpoint:     change.InitialCheckpoint.NextServerSeq(serverSeq),
		changeID:       change.InitialID().SetClocks(lamport, vector),
		detachedActors: detachedActors,
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

// SyncCheckpoint syncs the checkpoint and the changeID with the given serverSeq
// and clientSeq.
func (d *InternalDocument) SyncCheckpoint(serverSeq int64, clientSeq uint32) {
	d.changeID = change.NewID(
		clientSeq,
		serverSeq,
		d.changeID.Lamport(),
		d.changeID.ActorID(),
		d.VersionVector(),
	)
	d.checkpoint = d.checkpoint.SyncClientSeq(clientSeq)
}

// HasLocalChanges returns whether this document has local changes or not.
func (d *InternalDocument) HasLocalChanges() bool {
	return len(d.localChanges) > 0
}

// ApplyChangePack applies the given change pack into this document.
func (d *InternalDocument) ApplyChangePack(pack *change.Pack, disableGC bool) error {
	hasSnapshot := len(pack.Snapshot) > 0

	// 01. Process detached actors from server signal.
	if len(pack.DetachedActors) > 0 {
		d.AddDetachedActors(pack.DetachedActors)
	}

	// 02. Apply remote changes to both the cloneRoot and the document.
	if hasSnapshot {
		if err := d.applySnapshot(pack.Snapshot, pack.VersionVector); err != nil {
			return err
		}
	} else {
		if _, err := d.ApplyChanges(pack.Changes...); err != nil {
			return err
		}
	}

	// 03. Remove local changes applied to server.
	for d.HasLocalChanges() {
		c := d.localChanges[0]
		if c.ClientSeq() > pack.Checkpoint.ClientSeq {
			break
		}
		d.localChanges = d.localChanges[1:]
	}

	// 04. Update the checkpoint.
	d.checkpoint = d.checkpoint.Forward(pack.Checkpoint)

	// 05. Do Garbage collection.
	if !disableGC && pack.VersionVector != nil && !hasSnapshot {
		gcVV := pack.VersionVector.DeepCopy()
		d.AugmentVV(gcVV)
		if _, err := d.GarbageCollect(gcVV); err != nil {
			return err
		}
	}

	return nil
}

// GarbageCollect purge elements that were removed before the given time.
func (d *InternalDocument) GarbageCollect(vector time.VersionVector) (int, error) {
	return d.root.GarbageCollect(vector)
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
	return change.NewPack(d.key, cp, changes, d.VersionVector(), nil)
}

// SetActor sets actor into this document. This is also applied in the local
// changes the document has.
func (d *InternalDocument) SetActor(actor time.ActorID) {
	for _, c := range d.localChanges {
		c.SetActor(actor)
	}
	d.changeID = d.changeID.SetActor(actor)

	// TODO(hackerwins): We need to update the root object as well.
}

// Lamport returns the Lamport clock of this document.
func (d *InternalDocument) Lamport() int64 {
	return d.changeID.Lamport()
}

// ActorID returns ID of the actor currently editing the document.
func (d *InternalDocument) ActorID() time.ActorID {
	return d.changeID.ActorID()
}

// VersionVector returns the version vector of this document.
func (d *InternalDocument) VersionVector() time.VersionVector {
	return d.changeID.VersionVector()
}

// DetachedActors returns the detached actors map.
func (d *InternalDocument) DetachedActors() map[time.ActorID]int64 {
	return d.detachedActors
}

// AddDetachedActors stores the given detached actors and removes them from the document's VV.
func (d *InternalDocument) AddDetachedActors(actors map[time.ActorID]int64) {
	for actorID, lamport := range actors {
		d.detachedActors[actorID] = lamport
		d.changeID.VersionVector().Unset(actorID)
	}
}

// AugmentVV augments the given VV with detached actors' lamport values.
// This ensures correct causality detection and GC after VV cleanup.
func (d *InternalDocument) AugmentVV(vv time.VersionVector) {
	for actorID, lamport := range d.detachedActors {
		if _, ok := vv.Get(actorID); !ok {
			vv.Set(actorID, lamport)
		}
	}
}

// SetStatus sets the status of this document.
func (d *InternalDocument) SetStatus(status StatusType) {
	d.status = status
}

// IsAttached returns whether this document is attached or not.
func (d *InternalDocument) IsAttached() bool {
	return d.status == StatusAttached
}

// Root returns the root of this document.
func (d *InternalDocument) Root() *crdt.Root {
	return d.root
}

// DocSize returns the size of the document.
func (d *InternalDocument) DocSize() resource.DocSize {
	return d.root.DocSize()
}

// RootObject returns the root object.
func (d *InternalDocument) RootObject() *crdt.Object {
	return d.root.Object()
}

func (d *InternalDocument) applySnapshot(snapshot []byte, vector time.VersionVector) error {
	rootObj, presences, detachedActors, err := converter.BytesToSnapshot(snapshot)
	if err != nil {
		return err
	}

	d.root = crdt.NewRoot(rootObj)
	d.presences = presences
	d.detachedActors = detachedActors
	if d.detachedActors == nil {
		d.detachedActors = make(map[time.ActorID]int64)
	}

	// NOTE(chacha912): Documents created from snapshots were experiencing edit
	// restrictions due to low lamport values.
	// Previously, the code attempted to generate document lamport from ServerSeq.
	// However, after aligning lamport logic with the original research paper,
	// ServerSeq could potentially become smaller than the lamport value.
	// To resolve this, we initialize document's lamport by using the highest
	// lamport value stored in version vector as the starting point.
	d.changeID = d.changeID.SetClocks(vector.MaxLamport(), vector)

	return nil
}

// ApplyChanges applies remote changes to the document.
func (d *InternalDocument) ApplyChanges(changes ...*change.Change) ([]DocEvent, error) {
	var events []DocEvent
	for _, c := range changes {
		var hadPresence, wasOnline bool
		var prevPresence presence.Data
		clientID := c.ID().ActorID().String()

		if c.PresenceChange() != nil {
			hadPresence = d.presences.Has(clientID)
			_, wasOnline = d.onlineClients[clientID]
			prevPresence = d.Presence(clientID)
		}

		// Augment change VV with detached actors' lamport values so that
		// causality detection and GC work correctly after VV cleanup.
		d.AugmentVV(c.ID().VersionVector())

		if err := c.Execute(d.root, d.presences); err != nil {
			return nil, err
		}

		if c.PresenceChange() != nil {
			if c.PresenceChange().ChangeType == presence.Clear {
				d.RemoveOnlineClient(clientID)
			}
			if event := d.ReconcilePresence(clientID, hadPresence, wasOnline, prevPresence); event != nil {
				events = append(events, *event)
			}
		}

		d.changeID = d.changeID.SyncClocks(c.ID())
	}

	return events, nil
}

// MyPresence returns the presence of the actor currently editing the document.
func (d *InternalDocument) MyPresence() presence.Data {
	if d.status != StatusAttached {
		return presence.NewData()
	}
	p := d.presences.Load(d.changeID.ActorID().String())
	return p.DeepCopy()
}

// Presence returns the presence of the given client.
// If the client is not online, it returns nil.
func (d *InternalDocument) Presence(clientID string) presence.Data {
	if !d.onlineClients[clientID] {
		return nil
	}

	return d.presences.Load(clientID).DeepCopy()
}

// PresenceForTest returns the presence of the given client
// regardless of whether the client is online or not.
func (d *InternalDocument) PresenceForTest(clientID string) presence.Data {
	return d.presences.Load(clientID).DeepCopy()
}

// Presences returns the presence map of online clients.
func (d *InternalDocument) Presences() map[string]presence.Data {
	presences := make(map[string]presence.Data)
	for clientID := range d.onlineClients {
		p := d.presences.Load(clientID)
		if p == nil {
			continue
		}
		presences[clientID] = p.DeepCopy()
	}
	return presences
}

// AllPresences returns the presence map of all clients
// regardless of whether the client is online or not.
func (d *InternalDocument) AllPresences() map[string]presence.Data {
	return d.presences.ToMap()
}

// SetOnlineClients sets the online clients.
func (d *InternalDocument) SetOnlineClients(ids ...string) {
	d.onlineClients = make(map[string]bool)

	for _, id := range ids {
		d.onlineClients[id] = true
	}
}

// AddOnlineClient adds the given client to the online clients.
func (d *InternalDocument) AddOnlineClient(clientID string) {
	d.onlineClients[clientID] = true
}

// RemoveOnlineClient removes the given client from the online clients.
func (d *InternalDocument) RemoveOnlineClient(clientID string) {
	delete(d.onlineClients, clientID)
}

// ReconcilePresence compares the previous and current state of a client's
// presence/online status and returns the appropriate event to emit.
//
// State transition table:
//
//	(!hadP || !wasOn) → (hasP && isOn)  : WatchedEvent
//	(hadP && wasOn)   → (hasP && isOn)  : PresenceChangedEvent
//	(hadP && wasOn)   → (!hasP || !isOn): UnwatchedEvent
//	otherwise                           : no event (waiting)
func (d *InternalDocument) ReconcilePresence(
	clientID string,
	hadPresence bool,
	wasOnline bool,
	prevPresence presence.Data,
) *DocEvent {
	hasPresence := d.presences.Has(clientID)
	_, isOnline := d.onlineClients[clientID]

	if !hasPresence || !isOnline {
		if hadPresence && wasOnline {
			return &DocEvent{
				Type: UnwatchedEvent,
				Presences: map[string]presence.Data{
					clientID: prevPresence,
				},
			}
		}
		return nil
	}

	if !hadPresence || !wasOnline {
		return &DocEvent{
			Type: WatchedEvent,
			Presences: map[string]presence.Data{
				clientID: d.Presence(clientID),
			},
		}
	}

	return &DocEvent{
		Type: PresenceChangedEvent,
		Presences: map[string]presence.Data{
			clientID: d.Presence(clientID),
		},
	}
}

// ToDocument converts this document to Document.
func (d *InternalDocument) ToDocument() *Document {
	doc := New(d.key)
	doc.setInternalDoc(d)
	return doc
}

// DeepCopy creates a deep copy of this document.
func (d *InternalDocument) DeepCopy() (*InternalDocument, error) {
	root, err := d.root.DeepCopy()
	if err != nil {
		return nil, err
	}

	onlineClients := make(map[string]bool)
	for id := range d.onlineClients {
		onlineClients[id] = true
	}

	detachedActors := make(map[time.ActorID]int64)
	for id, lamport := range d.detachedActors {
		detachedActors[id] = lamport
	}

	return &InternalDocument{
		key:        d.key,
		status:     d.status,
		checkpoint: d.checkpoint,

		// TODO(hackerwins): Previously ChangeID used as an immutable value,
		// but now it is mutable, so we need to create a new instance of ChangeID.
		// COnsider removing this in the future.
		changeID: d.changeID.DeepCopy(),

		root:           root,
		presences:      d.presences.DeepCopy(),
		onlineClients:  onlineClients,
		localChanges:   d.localChanges,
		detachedActors: detachedActors,
	}, nil
}
