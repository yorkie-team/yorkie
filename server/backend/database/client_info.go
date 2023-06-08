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
	DocID     types.ID `bson:"doc_id"`
	Status    string   `bson:"status"`
	ServerSeq int64    `bson:"server_seq"`
	ClientSeq uint32   `bson:"client_seq"`
}

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

	// Documents is array of document which is attached to the client.
	Documents []*ClientDocInfo `bson:"documents"`

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
			i.ID.String(),
			i.ProjectID.String(),
			projectID.String(),
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
func (i *ClientInfo) AttachDocument(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("client(%s) attaches document(%s): %w", i.ID.String(), docID.String(), ErrClientNotActivated)
	}

	if i.Documents == nil {
		i.Documents = []*ClientDocInfo{}
	}

	documentInfo := i.FindDocumentInfo(docID)
	if documentInfo != nil && documentInfo.Status == DocumentAttached {
		return fmt.Errorf("client(%s) attaches document(%s): %w", i.ID.String(), docID.String(), ErrDocumentAlreadyAttached)
	}

	if documentInfo == nil {
		i.SetDocumentInfo(&ClientDocInfo{
			DocID:     docID,
			Status:    DocumentAttached,
			ServerSeq: 0,
			ClientSeq: 0,
		})
	} else {
		documentInfo.Status = DocumentAttached
	}
	i.UpdatedAt = time.Now()

	return nil
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttached(docID); err != nil {
		return fmt.Errorf("client(%s) detaches document(%s): %w", i.ID.String(), docID.String(), err)
	}

	i.SetDocumentInfo(&ClientDocInfo{
		DocID:     docID,
		Status:    DocumentDetached,
		ServerSeq: 0,
		ClientSeq: 0,
	})
	i.UpdatedAt = time.Now()

	return nil
}

// RemoveDocument removes the given document from this client.
func (i *ClientInfo) RemoveDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttached(docID); err != nil {
		return fmt.Errorf("client(%s) removes document(%s): %w", i.ID.String(), docID.String(), err)
	}

	i.SetDocumentInfo(&ClientDocInfo{
		DocID:     docID,
		Status:    DocumentRemoved,
		ServerSeq: 0,
		ClientSeq: 0,
	})
	i.UpdatedAt = time.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(docID types.ID) (bool, error) {
	documentInfo := i.FindDocumentInfo(docID)
	if documentInfo == nil {
		return false, fmt.Errorf("check document(%s) is attached: %w", docID.String(), ErrDocumentNeverAttached)
	}

	return documentInfo.Status == DocumentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(docID types.ID) change.Checkpoint {
	documentInfo := i.FindDocumentInfo(docID)
	if documentInfo == nil {
		return change.InitialCheckpoint
	}

	return change.NewCheckpoint(documentInfo.ServerSeq, documentInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	docID types.ID,
	cp change.Checkpoint,
) error {
	documentInfo := i.FindDocumentInfo(docID)
	if documentInfo == nil {
		return fmt.Errorf("update checkpoint in document(%s): %w", docID.String(), ErrDocumentNeverAttached)
	}

	documentInfo.ServerSeq = cp.ServerSeq
	documentInfo.ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached document(%s) in client(%s): %w",
			docID.String(),
			i.ID.String(),
			ErrClientNotActivated,
		)
	}

	documentInfo := i.FindDocumentInfo(docID)
	if documentInfo == nil || documentInfo.Status != DocumentAttached {
		return fmt.Errorf("ensure attached document(%s) in client(%s): %w",
			docID.String(),
			i.ID.String(),
			ErrDocumentNotAttached,
		)
	}

	return nil
}

// DeepCopy returns a deep copy of this client info.
func (i *ClientInfo) DeepCopy() *ClientInfo {
	if i == nil {
		return nil
	}

	var documents []*ClientDocInfo
	for _, v := range i.Documents {
		documents = append(documents, &ClientDocInfo{
			DocID:     v.DocID,
			Status:    v.Status,
			ServerSeq: v.ServerSeq,
			ClientSeq: v.ClientSeq,
		})
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

// FindDocumentInfo finds the document info by the given docID.
func (i *ClientInfo) FindDocumentInfo(docID types.ID) *ClientDocInfo {
	if len(i.Documents) == 0 {
		return nil
	}

	for _, docInfo := range i.Documents {
		if docInfo.DocID == docID {
			return docInfo
		}
	}

	return nil
}

// SetDocumentInfo sets the document info by the given docID.
func (i *ClientInfo) SetDocumentInfo(docInfo *ClientDocInfo) {
	for idx, v := range i.Documents {
		if v.DocID == docInfo.DocID {
			i.Documents[idx] = docInfo
			return
		}
	}

	i.Documents = append(i.Documents, docInfo)
}
