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

// PeerChangedEvent represents events that occur when the states of another peers
// of the watched documents changes.
type PeerChangedEvent struct {
	Type      PeerChangedEventType
	Publisher map[string]presence.Presence
}

// PeerChangedEventType represents the type of PeerChangedEvent.
type PeerChangedEventType string

const (
	// WatchedEvent is an event that occurs when document is watched by other clients.
	WatchedEvent PeerChangedEventType = "watched"

	// UnwatchedEvent is an event that occurs when document is unwatched by other clients.
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
	peerPresenceMap *gosync.Map // map[string]presence.Info
	watchedPeerMap  *gosync.Map // map[string]bool
	changeContext   *change.Context
	events          chan PeerChangedEvent
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
		changeID:        change.InitialIDWithActor(actorID),
		myClientID:      clientID,
		peerPresenceMap: &gosync.Map{},
		watchedPeerMap:  &gosync.Map{},
		events:          make(chan PeerChangedEvent, 1),
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

	presenceMap := map[string]presence.Info{}
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
		watchedPeerMap:  &gosync.Map{},
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

// InitPresence initializes the presence of the client who created this document.
func (d *InternalDocument) InitPresence(initialPresence presence.Presence) {
	copiedPresence := map[string]string{}
	for k, v := range initialPresence {
		copiedPresence[k] = v
	}
	initialPresenceInfo := presence.Info{
		Presence: copiedPresence,
	}
	d.peerPresenceMap.Store(d.myClientID, initialPresenceInfo)
	d.watchedPeerMap.Store(d.myClientID, true)

	d.changeContext = change.NewContext(
		d.changeID.Next(),
		"",
		nil,
	)
	d.changeContext.SetPresenceInfo(&initialPresenceInfo)
	c := d.changeContext.ToChange()
	d.localChanges = append(d.localChanges, c)
	d.changeID = d.changeContext.ID()
	d.changeContext = nil
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
	return change.NewPack(d.key, cp, changes, nil, "")
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
func (d *InternalDocument) SetPresenceInfo(clientID string, info presence.Info) bool {
	presenceInfo, ok := d.peerPresenceMap.Load(clientID)
	if !ok {
		d.peerPresenceMap.Store(clientID, info)
		return true
	}

	updatedInfo, ok := presenceInfo.(presence.Info)
	if ok && updatedInfo.Update(info) {
		d.peerPresenceMap.Store(clientID, updatedInfo)
		return true
	}
	return false
}

// PresenceInfo returns the presence information of the given client.
func (d *InternalDocument) PresenceInfo(clientID string) (*presence.Info, error) {
	if presenceInfo, ok := d.peerPresenceMap.Load(clientID); ok {
		if info, ok := presenceInfo.(presence.Info); ok {
			return &info, nil
		}
	}
	return nil, errors.New("presence info not found")
}

// RemovePresenceInfo removes the presence information of the given client.
func (d *InternalDocument) RemovePresenceInfo(clientID string) {
	d.peerPresenceMap.Delete(clientID)
}

// PresenceInfoMap converts the peerPresenceMap from gosync.Map to a map format.
func (d *InternalDocument) PresenceInfoMap() map[string]presence.Info {
	presenceMap := map[string]presence.Info{}
	d.peerPresenceMap.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		if presenceInfo, ok := value.(presence.Info); ok {
			presenceMap[clientID] = presenceInfo
		}
		return true
	})
	return presenceMap
}

// SetPresenceInfoMap sets the peerPresenceMap.
func (d *InternalDocument) SetPresenceInfoMap(peerMap map[string]presence.Info) {
	if d.peerPresenceMap == nil {
		d.peerPresenceMap = &gosync.Map{}
	}
	d.peerPresenceMap.Range(func(key, value interface{}) bool {
		d.peerPresenceMap.Delete(key)
		return true
	})
	for peer := range peerMap {
		d.peerPresenceMap.Store(peer, true)
	}
}

// SetWatchedPeerMap sets the watched peer map.
func (d *InternalDocument) SetWatchedPeerMap(peerMap map[string]bool) {
	if d.watchedPeerMap == nil {
		d.watchedPeerMap = &gosync.Map{}
	}
	d.watchedPeerMap.Range(func(key, value interface{}) bool {
		d.watchedPeerMap.Delete(key)
		return true
	})
	for peer := range peerMap {
		d.watchedPeerMap.Store(peer, true)
	}
}

// AddWatchedPeerMap adds the peer to the watched peer map.
func (d *InternalDocument) AddWatchedPeerMap(clientID string, hasPresence bool) {
	d.watchedPeerMap.Store(clientID, hasPresence)
}

// RemoveWatchedPeerMap removes the peer from the watched peer map.
func (d *InternalDocument) RemoveWatchedPeerMap(clientID string) {
	d.watchedPeerMap.Delete(clientID)
}

// Presence returns the presence of the client who created this document.
func (d *InternalDocument) Presence() presence.Presence {
	myPresence := make(presence.Presence)
	if presenceInfo, ok := d.peerPresenceMap.Load(d.myClientID); ok {
		if info, ok := presenceInfo.(presence.Info); ok {
			for k, v := range info.Presence {
				myPresence[k] = v
			}
		}
	}
	return myPresence
}

// PeerPresence returns the presence of the given client.
func (d *InternalDocument) PeerPresence(clientID string) presence.Presence {
	peerPresence := make(presence.Presence)
	if presenceInfo, ok := d.peerPresenceMap.Load(clientID); ok {
		if info, ok := presenceInfo.(presence.Info); ok {
			for k, v := range info.Presence {
				peerPresence[k] = v
			}
		}
	}
	return peerPresence
}

// PeersMap returns the peer presence map, including the client who created this document.
func (d *InternalDocument) PeersMap() map[string]presence.Presence {
	peers := map[string]presence.Presence{}
	d.peerPresenceMap.Range(func(key, value interface{}) bool {
		clientID := key.(string)
		presenceInfo, ok := value.(presence.Info)
		if ok {
			peers[clientID] = presence.Presence{}
			for k, v := range presenceInfo.Presence {
				peers[clientID][k] = v
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
		d.SetPresenceInfoMap(map[string]presence.Info{})
		return nil
	}

	var presenceInfoMap map[string]presence.Info
	if err := json.Unmarshal([]byte(snapshotPresence), &presenceInfoMap); err != nil {
		return fmt.Errorf("unmarshal presence map: %w", err)
	}
	d.SetPresenceInfoMap(presenceInfoMap)
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
func (d *InternalDocument) ApplyChanges(changes ...*change.Change) error {
	for _, c := range changes {
		if err := c.Execute(d.root); err != nil {
			return err
		}
		if c.PresenceInfo() != nil {
			clientID := c.ID().ActorID().String()

			// TODO(chacha912): Verify where to send the PeerChangedEvent
			peer, ok := d.watchedPeerMap.Load(clientID)
			if !ok {
				d.SetPresenceInfo(clientID, *c.PresenceInfo())
			} else if !peer.(bool) {
				d.watchedPeerMap.Store(clientID, true)
				d.SetPresenceInfo(clientID, *c.PresenceInfo())
				d.events <- PeerChangedEvent{
					Type: WatchedEvent,
					Publisher: map[string]presence.Presence{
						clientID: d.PeerPresence(clientID),
					},
				}
			} else {
				isUpdated := d.SetPresenceInfo(clientID, *c.PresenceInfo())
				if isUpdated && clientID != d.myClientID {
					d.events <- PeerChangedEvent{
						Type: PresenceChangedEvent,
						Publisher: map[string]presence.Presence{
							clientID: d.PeerPresence(clientID),
						},
					}
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
