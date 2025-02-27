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

package database

import (
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// ConnectedClientInfo represents information about a connected client
type ConnectedClientInfo struct {
	// ConnectedAt is the time when the client connected to the document
	ConnectedAt time.Time `bson:"connected_at"`
}

// ConnectedClientsMap is a map that associates client IDs with their connection information
type ConnectedClientsMap map[types.ID]*ConnectedClientInfo

// DocInfo is a structure representing information of the document.
type DocInfo struct {
	// ID is the unique ID of the document.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project that the document belongs to.
	ProjectID types.ID `bson:"project_id"`

	// Key is the key of the document.
	Key key.Key `bson:"key"`

	// ServerSeq is the sequence number of the last change of the document on the server.
	ServerSeq int64 `bson:"server_seq"`

	// Owner is the owner(ID of the client) of the document.
	Owner types.ID `bson:"owner"`

	// ConnectedClients stores the clients currently connected to this document.
	ConnectedClients ConnectedClientsMap `bson:"connected_clients"`

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time `bson:"created_at"`

	// AccessedAt is the time when the document is accessed.
	AccessedAt time.Time `bson:"accessed_at"`

	// UpdatedAt is the time when the document is updated.
	UpdatedAt time.Time `bson:"updated_at"`

	// RemovedAt is the time when the document is removed.
	RemovedAt time.Time `bson:"removed_at"`
}

// IncreaseServerSeq increases server sequence of the document.
func (info *DocInfo) IncreaseServerSeq() int64 {
	info.ServerSeq++
	return info.ServerSeq
}

// IsRemoved returns true if the document is removed
func (info *DocInfo) IsRemoved() bool {
	return !info.RemovedAt.IsZero()
}

// AddConnectedClient adds a client to the connected clients map.
func (info *DocInfo) AddConnectedClient(clientID types.ID) *ConnectedClientInfo {
	if info.ConnectedClients == nil {
		info.ConnectedClients = make(ConnectedClientsMap)
	}
	info.ConnectedClients[clientID] = &ConnectedClientInfo{
		ConnectedAt: time.Now(),
	}
	return info.ConnectedClients[clientID]
}

// RemoveConnectedClient removes a client from the connected clients map.
func (info *DocInfo) RemoveConnectedClient(clientID types.ID) {
	if info.ConnectedClients != nil {
		delete(info.ConnectedClients, clientID)
	}
}

// IsClientConnected checks if a client is connected to this document.
func (info *DocInfo) IsClientConnected(clientID types.ID) bool {
	if info.ConnectedClients == nil {
		return false
	}
	_, exists := info.ConnectedClients[clientID]
	return exists
}

// GetConnectedClientCount returns the number of clients connected to this document.
func (info *DocInfo) GetConnectedClientCount() int {
	if info.ConnectedClients == nil {
		return 0
	}
	return len(info.ConnectedClients)
}

// DeepCopy creates a deep copy of this DocInfo.
func (info *DocInfo) DeepCopy() *DocInfo {
	if info == nil {
		return nil
	}

	copiedConnectedClients := make(ConnectedClientsMap)
	for clientID, clientInfo := range info.ConnectedClients {
		copiedConnectedClients[clientID] = &ConnectedClientInfo{
			ConnectedAt: clientInfo.ConnectedAt,
		}
	}

	return &DocInfo{
		ID:               info.ID,
		ProjectID:        info.ProjectID,
		Key:              info.Key,
		ServerSeq:        info.ServerSeq,
		Owner:            info.Owner,
		ConnectedClients: copiedConnectedClients,
		CreatedAt:        info.CreatedAt,
		AccessedAt:       info.AccessedAt,
		UpdatedAt:        info.UpdatedAt,
		RemovedAt:        info.RemovedAt,
	}
}

// RefKey returns the refKey of the document.
func (info *DocInfo) RefKey() types.DocRefKey {
	return types.DocRefKey{
		ProjectID: info.ProjectID,
		DocID:     info.ID,
	}
}
