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

package db

import (
	goerrors "errors"
	"time"

	"github.com/pkg/errors"

	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
)

// Below are the errors may occur depending on the document and client status.
var (
	ErrClientNotActivated      = goerrors.New("client not activated")
	ErrDocumentNotAttached     = goerrors.New("document not attached")
	ErrDocumentNeverAttached   = goerrors.New("client has never attached the document")
	ErrDocumentAlreadyAttached = goerrors.New("document already attached")
)

// Below are statuses of the client.
const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

const (
	documentAttached = "attached"
	documentDetached = "detached"
)

// ClientDocInfo is a structure representing information of the document
// attached to the client.
type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq uint64 `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
}

// ClientInfo is a structure representing information of a client.
type ClientInfo struct {
	ID        ID                    `bson:"_id_fake"`
	Key       string                `bson:"key"`
	Status    string                `bson:"status"`
	Documents map[ID]*ClientDocInfo `bson:"documents"`
	CreatedAt time.Time             `bson:"created_at"`
	UpdatedAt time.Time             `bson:"updated_at"`
}

// AttachDocument attaches the given document to this client.
func (i *ClientInfo) AttachDocument(docID ID) error {
	if i.Status != ClientActivated {
		return errors.WithStack(ErrClientNotActivated)
	}

	if i.Documents == nil {
		i.Documents = make(map[ID]*ClientDocInfo)
	}

	if i.hasDocument(docID) && i.Documents[docID].Status == documentAttached {
		return errors.WithStack(ErrDocumentAlreadyAttached)
	}

	i.Documents[docID] = &ClientDocInfo{
		Status:    documentAttached,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	i.UpdatedAt = time.Now()

	return nil
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(docID ID) error {
	if err := i.EnsureDocumentAttached(docID); err != nil {
		return err
	}

	i.Documents[docID].Status = documentDetached
	i.Documents[docID].ClientSeq = 0
	i.Documents[docID].ServerSeq = 0
	i.UpdatedAt = time.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(docID ID) (bool, error) {
	if !i.hasDocument(docID) {
		return false, errors.WithStack(ErrDocumentNeverAttached)
	}

	return i.Documents[docID].Status == documentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(docID ID) *checkpoint.Checkpoint {
	clientDocInfo := i.Documents[docID]
	if clientDocInfo == nil {
		return checkpoint.Initial
	}

	return checkpoint.New(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	docID ID,
	cp *checkpoint.Checkpoint,
) error {
	if !i.hasDocument(docID) {
		return errors.WithStack(ErrDocumentNeverAttached)
	}

	i.Documents[docID].ServerSeq = cp.ServerSeq
	i.Documents[docID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(docID ID) error {
	if i.Status != ClientActivated {
		return errors.WithStack(ErrClientNotActivated)
	}

	if !i.hasDocument(docID) || i.Documents[docID].Status == documentDetached {
		return errors.WithStack(ErrDocumentNotAttached)
	}

	return nil
}

func (i *ClientInfo) hasDocument(docID ID) bool {
	return i.Documents != nil && i.Documents[docID] != nil
}
