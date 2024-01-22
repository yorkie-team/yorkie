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
	"errors"
	"fmt"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
)

// Below are the errors may occur depending on the document and client status.
var (
	ErrClientNotActivated      = errors.New("client not activated")
	ErrDocumentNotAttached     = errors.New("document not attached")
	ErrDocumentNeverAttached   = errors.New("client has never attached the document")
	ErrDocumentAlreadyAttached = errors.New("document already attached")
)

// Below are statuses of the client.
const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

// Below are statuses of the document.
const (
	DocumentAttached = "attached"
	DocumentDetached = "detached"
	DocumentRemoved  = "removed"
)

// ClientDocInfo is a structure representing information of the document
// attached to the client.
type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq int64  `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
}

// ClientDocInfoMap is a map that associates DocRefKey with ClientDocInfo instances.
type ClientDocInfoMap map[types.DocRefKey]*ClientDocInfo

// ClientInfo is a structure representing information of a client.
type ClientInfo struct {
	// ID is the unique ID of the client.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project the client belongs to.
	ProjectID types.ID `bson:"project_id"`

	// Key is the key of the client. It is used to identify the client by users.
	Key string `bson:"key"`

	// Status is the status of the client.
	Status string `bson:"status"`

	// Documents is a map of document which is attached to the client.
	Documents ClientDocInfoMap `bson:"documents"`

	// CreatedAt is the time when the client was created.
	CreatedAt time.Time `bson:"created_at"`

	// UpdatedAt is the last time the client was accessed.
	// NOTE(hackerwins): The field name is "updated_at" but it is used as
	// "accessed_at".
	UpdatedAt time.Time `bson:"updated_at"`
}

// CheckIfInProject checks if the client is in the project.
func (i *ClientInfo) CheckIfInProject(projectID types.ID) error {
	if i.ProjectID != projectID {
		return fmt.Errorf(
			"check client(%s,%s) in project(%s): %w",
			i.ID,
			i.ProjectID,
			projectID,
			ErrClientNotFound,
		)
	}
	return nil
}

// Deactivate sets the status of this client to be deactivated.
func (i *ClientInfo) Deactivate() {
	i.Status = ClientDeactivated
	i.UpdatedAt = time.Now()
}

// AttachDocument attaches the given document to this client.
func (i *ClientInfo) AttachDocument(refKey types.DocRefKey) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, refKey, ErrClientNotActivated)
	}

	if i.Documents == nil {
		i.Documents = make(map[types.DocRefKey]*ClientDocInfo)
	}

	if i.hasDocument(refKey) && i.Documents[refKey].Status == DocumentAttached {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, refKey, ErrDocumentAlreadyAttached)
	}

	i.Documents[refKey] = &ClientDocInfo{
		Status:    DocumentAttached,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	i.UpdatedAt = time.Now()

	return nil
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(refKey types.DocRefKey) error {
	if err := i.EnsureDocumentAttached(refKey); err != nil {
		return err
	}

	i.Documents[refKey].Status = DocumentDetached
	i.Documents[refKey].ClientSeq = 0
	i.Documents[refKey].ServerSeq = 0
	i.UpdatedAt = time.Now()

	return nil
}

// RemoveDocument removes the given document from this client.
func (i *ClientInfo) RemoveDocument(refKey types.DocRefKey) error {
	if err := i.EnsureDocumentAttached(refKey); err != nil {
		return err
	}

	i.Documents[refKey].Status = DocumentRemoved
	i.Documents[refKey].ClientSeq = 0
	i.Documents[refKey].ServerSeq = 0
	i.UpdatedAt = time.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(refKey types.DocRefKey) (bool, error) {
	if !i.hasDocument(refKey) {
		return false, fmt.Errorf("check %s is attached: %w",
			refKey, ErrDocumentNeverAttached)
	}

	return i.Documents[refKey].Status == DocumentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(refKey types.DocRefKey) change.Checkpoint {
	clientDocInfo := i.Documents[refKey]
	if clientDocInfo == nil {
		return change.InitialCheckpoint
	}

	return change.NewCheckpoint(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	refKey types.DocRefKey,
	cp change.Checkpoint,
) error {
	if !i.hasDocument(refKey) {
		return fmt.Errorf("update checkpoint in %s: %w", refKey, ErrDocumentNeverAttached)
	}

	i.Documents[refKey].ServerSeq = cp.ServerSeq
	i.Documents[refKey].ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(refKey types.DocRefKey) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			refKey, i.ID, ErrClientNotActivated)
	}

	if !i.hasDocument(refKey) || i.Documents[refKey].Status != DocumentAttached {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			refKey, i.ID, ErrDocumentNotAttached)
	}

	return nil
}

// DeepCopy returns a deep copy of this client info.
func (i *ClientInfo) DeepCopy() *ClientInfo {
	if i == nil {
		return nil
	}

	documents := make(map[types.DocRefKey]*ClientDocInfo, len(i.Documents))
	for refKey, docInfo := range i.Documents {
		documents[refKey] = &ClientDocInfo{
			Status:    docInfo.Status,
			ServerSeq: docInfo.ServerSeq,
			ClientSeq: docInfo.ClientSeq,
		}
	}

	return &ClientInfo{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		Key:       i.Key,
		Status:    i.Status,
		Documents: documents,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}
}

func (i *ClientInfo) hasDocument(refKey types.DocRefKey) bool {
	return i.Documents != nil && i.Documents[refKey] != nil
}
