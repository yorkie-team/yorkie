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
	"fmt"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/errors"
)

// Below are the errors may occur depending on the document and client status.
var (
	ErrClientNotActivated      = errors.FailedPrecond("client not activated").WithCode("ErrClientNotActivated")
	ErrDocumentNotAttached     = errors.FailedPrecond("document not attached").WithCode("ErrDocumentNotAttached")
	ErrDocumentNeverAttached   = errors.FailedPrecond("document never attached").WithCode("ErrDocumentNeverAttached")
	ErrDocumentAlreadyAttached = errors.FailedPrecond("document already attached").WithCode("ErrDocumentAlreadyAttached")
	ErrDocumentAlreadyDetached = errors.FailedPrecond("document already detached").WithCode("ErrDocumentAlreadyDetached")
	ErrAttachedDocumentExists  = errors.FailedPrecond("attached document exists").WithCode("ErrAttachedDocumentExists")
)

// Below are statuses of the client.
const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

// Below are statuses of the document.
const (
	DocumentAttaching = "attaching"
	DocumentAttached  = "attached"
	DocumentDetached  = "detached"
	DocumentRemoved   = "removed"
)

// ClientDocInfo is a structure representing information of the document
// attached to the client.
type ClientDocInfo struct {
	Status    string `bson:"status"`
	OpSeq     int64  `bson:"op_seq"`
	PrSeq     int64  `bson:"pr_seq"`
	ClientSeq uint32 `bson:"client_seq"`
}

// GetServerSeq returns the server sequence as ServerSeq struct.
func (i *ClientDocInfo) GetServerSeq() change.ServerSeq {
	return change.NewServerSeq(i.OpSeq, i.PrSeq)
}

// ClientDocInfoMap is a map that associates DocRefKey with ClientDocInfo instances.
type ClientDocInfoMap map[types.ID]*ClientDocInfo

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

	// Metadata is the metadata of the client.
	Metadata map[string]string `bson:"metadata"`

	// CreatedAt is the time when the client was created.
	CreatedAt gotime.Time `bson:"created_at"`

	// UpdatedAt is the last time the client was accessed.
	// NOTE(hackerwins): The field name is "updated_at" but it is used as
	// "accessed_at".
	UpdatedAt gotime.Time `bson:"updated_at"`
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
	i.UpdatedAt = gotime.Now()
}

// AttachDocument attaches the given document to this client.
func (i *ClientInfo) AttachDocument(docID types.ID, alreadyAttached bool) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrClientNotActivated)
	}

	if i.Documents == nil {
		i.Documents = make(map[types.ID]*ClientDocInfo)
	}

	if i.IsAlreadyDetached(docID, alreadyAttached) {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrDocumentAlreadyDetached)
	}

	if i.hasDocument(docID) && i.Documents[docID].Status == DocumentAttached {
		return fmt.Errorf("client(%s) attaches %s: %w",
			i.ID, docID, ErrDocumentAlreadyAttached)
	}

	i.Documents[docID] = &ClientDocInfo{
		Status:    DocumentAttached,
		OpSeq:     0,
		PrSeq:     0,
		ClientSeq: 0,
	}
	i.UpdatedAt = gotime.Now()

	return nil
}

// IsAlreadyDetached checks if the document is already detached.
func (i *ClientInfo) IsAlreadyDetached(docID types.ID, alreadyAttached bool) bool {
	if alreadyAttached && i.hasDocument(docID) && i.Documents[docID].Status == DocumentDetached {
		return true
	}
	return false
}

// IsAttaching checks if the document is attaching.
func (i *ClientInfo) IsAttaching(docID types.ID) bool {
	if i.hasDocument(docID) && i.Documents[docID].Status == DocumentAttaching {
		return true
	}
	return false
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttachedOrAttaching(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = DocumentDetached
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].OpSeq = 0
	i.Documents[docID].PrSeq = 0
	i.UpdatedAt = gotime.Now()

	return nil
}

// RemoveDocument removes the given document from this client.
func (i *ClientInfo) RemoveDocument(docID types.ID) error {
	if err := i.EnsureDocumentAttached(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = DocumentRemoved
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].OpSeq = 0
	i.Documents[docID].PrSeq = 0
	i.UpdatedAt = gotime.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(docID types.ID) (bool, error) {
	if !i.hasDocument(docID) {
		return false, fmt.Errorf("check %s is attached: %w",
			docID, ErrDocumentNeverAttached)
	}

	return i.Documents[docID].Status == DocumentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(docID types.ID) change.Checkpoint {
	clientDocInfo := i.Documents[docID]
	if clientDocInfo == nil {
		return change.InitialCheckpoint
	}

	return change.NewCheckpoint(change.NewServerSeq(clientDocInfo.OpSeq, clientDocInfo.PrSeq), clientDocInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	docID types.ID,
	cp change.Checkpoint,
) error {
	if !i.hasDocument(docID) {
		return fmt.Errorf("update checkpoint in %s: %w", docID, ErrDocumentNeverAttached)
	}

	i.Documents[docID].OpSeq = cp.ServerSeq.OpSeq
	i.Documents[docID].PrSeq = cp.ServerSeq.PrSeq
	i.Documents[docID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = gotime.Now()

	return nil
}

// ServerSeq returns the server sequence of the given document.
func (i *ClientInfo) ServerSeq(
	docID types.ID,
) (int64, error) {
	if !i.hasDocument(docID) {
		return 0, fmt.Errorf("document not found %s: %w", docID, ErrDocumentNotFound)
	}

	clientDocInfo := i.Documents[docID]
	return max(clientDocInfo.OpSeq, clientDocInfo.PrSeq), nil
}

// EnsureActivated ensures the client is activated.
func (i *ClientInfo) EnsureActivated() error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure activated client(%s): %w", i.ID, ErrClientNotActivated)
	}

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			docID, i.ID, ErrClientNotActivated)
	}

	if !i.hasDocument(docID) || i.Documents[docID].Status != DocumentAttached {
		return fmt.Errorf("ensure attached %s in client(%s): %w",
			docID, i.ID, ErrDocumentNotAttached)
	}

	return nil
}

// EnsureDocumentAttachedOrAttaching ensures the given document is attached or attaching.
func (i *ClientInfo) EnsureDocumentAttachedOrAttaching(docID types.ID) error {
	if i.Status != ClientActivated {
		return fmt.Errorf("ensure attached or attaching %s in client(%s): %w",
			docID, i.ID, ErrClientNotActivated)
	}

	if !i.hasDocument(docID) ||
		(i.Documents[docID].Status != DocumentAttached &&
			i.Documents[docID].Status != DocumentAttaching) {
		return fmt.Errorf("ensure attached or attaching %s in client(%s): %w",
			docID, i.ID, ErrDocumentNotAttached)
	}

	return nil
}

// EnsureDocumentsNotAttachedWhenDeactivated ensures that no documents are attached
// when the client is deactivated.
func (i *ClientInfo) EnsureDocumentsNotAttachedWhenDeactivated() error {
	if i.Status != ClientDeactivated {
		return nil
	}

	for docID := range i.Documents {
		isAttached, err := i.IsAttached(docID)
		if err != nil {
			return err
		}

		if isAttached {
			return ErrAttachedDocumentExists
		}
	}

	return nil
}

// DeepCopy returns a deep copy of this client info.
func (i *ClientInfo) DeepCopy() *ClientInfo {
	if i == nil {
		return nil
	}

	documents := make(map[types.ID]*ClientDocInfo, len(i.Documents))
	for docID, docInfo := range i.Documents {
		documents[docID] = &ClientDocInfo{
			Status:    docInfo.Status,
			OpSeq:     docInfo.OpSeq,
			PrSeq:     docInfo.PrSeq,
			ClientSeq: docInfo.ClientSeq,
		}
	}

	return &ClientInfo{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		Key:       i.Key,
		Status:    i.Status,
		Documents: documents,
		Metadata:  i.Metadata,
		CreatedAt: i.CreatedAt,
		UpdatedAt: i.UpdatedAt,
	}
}

func (i *ClientInfo) hasDocument(docID types.ID) bool {
	return i.Documents != nil && i.Documents[docID] != nil
}

// RefKey returns the refKey of the client.
func (i *ClientInfo) RefKey() types.ClientRefKey {
	return types.ClientRefKey{
		ProjectID: i.ProjectID,
		ClientID:  i.ID,
	}
}

// IsServerClient returns true if this client represents a server‐side process.
func (i *ClientInfo) IsServerClient() bool {
	actorID, err := i.ID.ToActorID()
	if err != nil {
		return false
	}

	return actorID == time.InitialActorID
}

// UpdateDocStatus updates the status of the document in the client info.
func (i *ClientInfo) UpdateDocStatus(
	docID types.ID,
	status document.StatusType,
	cp change.Checkpoint,
) error {
	switch status {
	case document.StatusRemoved:
		return i.RemoveDocument(docID)
	case document.StatusDetached:
		return i.DetachDocument(docID)
	default:
		return i.UpdateCheckpoint(docID, cp)
	}
}

// AttachedDocuments returns the list of document IDs attached to this client.
func (i *ClientInfo) AttachedDocuments() []types.ID {
	if i.Documents == nil {
		return nil
	}

	docIDs := make([]types.ID, 0, len(i.Documents))
	for docID := range i.Documents {
		if i.Documents[docID].Status != DocumentAttached {
			continue
		}

		docIDs = append(docIDs, docID)
	}

	return docIDs
}
