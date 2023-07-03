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
	"encoding/json"
	"errors"
	"fmt"
	gosync "sync"

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

// InternalDocument represents a document in MongoDB and contains logical clocks.
type InternalDocument struct {
	key    key.Key
	status StatusType

	root         *crdt.Root
	checkpoint   change.Checkpoint
	changeID     change.ID
	localChanges []*change.Change

	// `watchedPeerSet` is a set of the peers that watch the document.
	// It is used to determine whether the peer is online or offline.
	// It uses a map as a set-like data, so the value is not meaningful.
	watchedPeerSet  *gosync.Map // map[string]bool
	peerPresenceMap *gosync.Map // map[string]presence.Presence

	changeContext *change.Context
}

// NewInternalDocument creates a new instance of InternalDocument.
func NewInternalDocument(docKey key.Key, clientID string) *InternalDocument {
	root := crdt.NewObject(crdt.NewElementRHT(), time.InitialTicket)
	actorID, _ := time.ActorIDFromHex(clientID)
	return &InternalDocument{
		key:             docKey,
		status:          StatusDetached,
		root:            crdt.NewRoot(root),
		checkpoint:      change.InitialCheckpoint,
		changeID:        change.InitialChangeIDOf(actorID),
		peerPresenceMap: &gosync.Map{},
		watchedPeerSet:  &gosync.Map{},
	}
}

// NewInternalDocumentFromSnapshot creates a new instance of InternalDocument with the snapshot.
func NewInternalDocumentFromSnapshot(
	k key.Key,
	serverSeq int64,
	lamport int64,
	snapshot []byte,
	snapshotPresence string,
) (*InternalDocument, error) {
	obj, err := converter.BytesToObject(snapshot)
	if err != nil {
		return nil, err
	}

	presenceMap := map[string]presence.Presence{}
	if snapshotPresence != "" {
		if err := json.Unmarshal([]byte(snapshotPresence), &presenceMap); err != nil {
			return nil, fmt.Errorf("unmarshal presence map: %w", err)
		}
	}
	presenceSyncMap := &gosync.Map{}
	for k, v := range presenceMap {
		presenceSyncMap.Store(k, v)
	}

	return &InternalDocument{
		key:             k,
		status:          StatusDetached,
		root:            crdt.NewRoot(obj),
		checkpoint:      change.InitialCheckpoint.NextServerSeq(serverSeq),
		changeID:        change.InitialID.SyncLamport(lamport),
		peerPresenceMap: presenceSyncMap,
		watchedPeerSet:  &gosync.Map{},
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

// myClientID returns the actor ID of the client that attaches this document.
func (d *InternalDocument) myClientID() string {
	return d.changeID.ActorID().String()
}

// InitPresence initializes the presence of the client who created this document.
func (d *InternalDocument) InitPresence(initialPresence presence.Presence) {
	copiedPresence := presence.Presence{}
	for k, v := range initialPresence {
		copiedPresence[k] = v
	}
	d.peerPresenceMap.Store(d.myClientID(), copiedPresence)
	d.AddWatchedPeerSet(d.myClientID())

	context := change.NewContext(
		d.changeID.Next(),
		"",
		nil,
	)
	context.SetPresence(&copiedPresence)
	c := context.ToChange()
	d.localChanges = append(d.localChanges, c)
	d.changeID = context.ID()
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
		if err := d.applySnapshotPresence(pack.SnapshotPresence); err != nil {
			return err
		}
	} else {
		if _, err := d.ApplyChanges(pack.Changes...); err != nil {
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
	return change.NewPack(d.key, cp, changes, nil, "")
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

// UpdatePresence updates the presence of the client who created this document.
func (d *InternalDocument) UpdatePresence(k, v string) error {
	p, ok := d.peerPresenceMap.Load(d.myClientID())
	if !ok {
		return errors.New("presence not found")
	}
	updatedPresence, ok := p.(presence.Presence)
	if !ok {
		return errors.New("invalid presence type")
	}
	updatedPresence[k] = v
	d.peerPresenceMap.Store(d.myClientID(), updatedPresence)
	return nil
}

// SetPresence sets the presence of the given client.
func (d *InternalDocument) SetPresence(clientID string, info presence.Presence) {
	d.peerPresenceMap.Store(clientID, info)
}

// HasPresence returns whether the peer presence exists regardless of
// whether the client is watching the document or not.
func (d *InternalDocument) HasPresence(clientID string) bool {
	if p, ok := d.peerPresenceMap.Load(clientID); ok {
		if _, ok := p.(presence.Presence); ok {
			return true
		}
	}
	return false
}

// PresenceMap converts the peerPresenceMap from gosync.Map to a map format.
func (d *InternalDocument) PresenceMap() map[string]presence.Presence {
	presenceMap := map[string]presence.Presence{}
	d.peerPresenceMap.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		if p, ok := value.(presence.Presence); ok {
			presenceMap[clientID] = p
		}
		return true
	})
	return presenceMap
}

// SetPresenceMap sets the peerPresenceMap.
func (d *InternalDocument) SetPresenceMap(peerMap map[string]presence.Presence) {
	if d.peerPresenceMap == nil {
		d.peerPresenceMap = &gosync.Map{}
	}
	d.peerPresenceMap.Range(func(key, value interface{}) bool {
		d.peerPresenceMap.Delete(key)
		return true
	})
	for peer, presence := range peerMap {
		d.peerPresenceMap.Store(peer, presence)
	}
}

// SetWatchedPeerSet sets the watched peer set.
func (d *InternalDocument) SetWatchedPeerSet(peerSet map[string]bool) {
	if d.watchedPeerSet == nil {
		d.watchedPeerSet = &gosync.Map{}
	}
	d.watchedPeerSet.Range(func(key, value interface{}) bool {
		d.watchedPeerSet.Delete(key)
		return true
	})
	for peer := range peerSet {
		d.AddWatchedPeerSet(peer)
	}
}

// AddWatchedPeerSet adds the peer to the watched peer set.
func (d *InternalDocument) AddWatchedPeerSet(clientID string) {
	d.watchedPeerSet.Store(clientID, true)
}

// RemoveWatchedPeerSet removes the peer from the watched peer set.
func (d *InternalDocument) RemoveWatchedPeerSet(clientID string) {
	d.watchedPeerSet.Delete(clientID)
}

// Presence returns the presence of the client who created this document.
func (d *InternalDocument) Presence() presence.Presence {
	return d.PeerPresence(d.myClientID())
}

// PeerPresence returns the presence of the given client.
func (d *InternalDocument) PeerPresence(clientID string) presence.Presence {
	if _, ok := d.watchedPeerSet.Load(clientID); !ok {
		return nil
	}

	peerPresence := make(presence.Presence)
	if val, ok := d.peerPresenceMap.Load(clientID); ok {
		if p, ok := val.(presence.Presence); ok {
			for k, v := range p {
				peerPresence[k] = v
			}
		}
	}
	return peerPresence
}

// PeersMap returns the peer presence map, including the client who created this document.
func (d *InternalDocument) PeersMap() map[string]presence.Presence {
	peers := map[string]presence.Presence{}
	d.watchedPeerSet.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		if val, ok := d.peerPresenceMap.Load(clientID); ok {
			if p, ok := val.(presence.Presence); ok {
				peers[clientID] = presence.Presence{}
				for k, v := range p {
					peers[clientID][k] = v
				}
			}
		}
		return true
	})
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

// ApplySnapshotPresence applies the snapshot presence to the document.
func (d *InternalDocument) applySnapshotPresence(snapshotPresence string) error {
	if snapshotPresence == "" {
		d.SetPresenceMap(map[string]presence.Presence{})
		return nil
	}

	var presenceMap map[string]presence.Presence
	if err := json.Unmarshal([]byte(snapshotPresence), &presenceMap); err != nil {
		return fmt.Errorf("unmarshal presence map: %w", err)
	}
	d.SetPresenceMap(presenceMap)
	return nil
}

// ApplySnapshot applies the snapshot to the document.
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
func (d *InternalDocument) ApplyChanges(changes ...*change.Change) ([]PeerChangedEvent, error) {
	events := []PeerChangedEvent{}
	for _, c := range changes {
		if err := c.Execute(d.root); err != nil {
			return nil, err
		}
		if c.Presence() != nil {
			newPresence := *c.Presence()
			clientID := c.ID().ActorID().String()
			_, ok := d.watchedPeerSet.Load(clientID)
			if ok {
				if d.HasPresence(clientID) {
					events = append(events, PeerChangedEvent{
						Type: PresenceChangedEvent,
						Peer: map[string]presence.Presence{
							clientID: newPresence,
						},
					})
				} else {
					d.AddWatchedPeerSet(clientID)
					events = append(events, PeerChangedEvent{
						Type: WatchedEvent,
						Peer: map[string]presence.Presence{
							clientID: newPresence,
						},
					})
				}
			}

			d.SetPresence(clientID, newPresence)
		}

		d.changeID = d.changeID.SyncLamport(c.ID().Lamport())
	}

	return events, nil
}
