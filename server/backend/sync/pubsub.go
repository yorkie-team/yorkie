/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

package sync

import (
	"github.com/rs/xid"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
)

// Subscription represents a subscription of a subscriber to documents.
type Subscription struct {
	id         string
	subscriber types.Client
	closed     bool
	events     chan DocEvent
}

// NewSubscription creates a new instance of Subscription.
func NewSubscription(subscriber types.Client) *Subscription {
	return &Subscription{
		id:         xid.New().String(),
		subscriber: subscriber,
		events:     make(chan DocEvent, 1),
	}
}

// ID returns the id of this subscription.
func (s *Subscription) ID() string {
	return s.id
}

// PeerSyncMap is a map that represents PeerSyncData for each peer.
type PeerSyncMap map[string]PeerSyncData

// PeerSyncData represents the data needed to synchronize peer presence.
type PeerSyncData struct {
	PresenceInfo       presence.PresenceInfo
	PendingUpdatePeers PeerSet
}

// UpdatePresence updates the presence of the client.
func (p PeerSyncMap) UpdatePresence(client *types.Client) {
	if cli, ok := p[client.ID.String()]; ok {
		cli.PresenceInfo.Update(client.PresenceInfo)
		p[client.ID.String()] = cli
	}
}

// AddPendingUpdatePeer adds the target peer to the PendingUpdatePeers of the client.
func (p PeerSyncMap) AddPendingUpdatePeer(clientID string, targetID string) {
	pendingUpdatePeers := p[clientID].PendingUpdatePeers
	if !pendingUpdatePeers.Contains(targetID) {
		pendingUpdatePeers.Add(targetID)
	}
}

// PeerSet represents a set of peers.
type PeerSet map[string]bool

func NewPeerSet() PeerSet {
	return make(map[string]bool)
}

func (s PeerSet) Add(key string) {
	s[key] = true
}

func (s PeerSet) Remove(key string) {
	delete(s, key)
}

func (s PeerSet) Contains(key string) bool {
	_, exists := s[key]
	return exists
}

// DocEvent represents events that occur related to the document.
type DocEvent struct {
	Type       types.DocEventType
	Publisher  types.Client
	DocumentID types.ID
}

// Events returns the DocEvent channel of this subscription.
func (s *Subscription) Events() chan DocEvent {
	return s.events
}

// Subscriber returns the subscriber of this subscription.
func (s *Subscription) Subscriber() types.Client {
	return s.subscriber
}

// SubscriberID returns string representation of the subscriber.
func (s *Subscription) SubscriberID() string {
	return s.subscriber.ID.String()
}

// Close closes all resources of this Subscription.
func (s *Subscription) Close() {
	if s.closed {
		return
	}

	s.closed = true
	close(s.events)
}
