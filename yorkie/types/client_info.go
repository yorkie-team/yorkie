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

package types

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
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
	ID        primitive.ObjectID        `bson:"_id"`
	Key       string                    `bson:"key"`
	Status    string                    `bson:"status"`
	Documents map[string]*ClientDocInfo `bson:"documents"`
	CreatedAt time.Time                 `bson:"created_at"`
	UpdatedAt time.Time                 `bson:"updated_at"`
}

// AttachDocument attaches the given document to this client.
func (i *ClientInfo) AttachDocument(docID primitive.ObjectID) error {
	if i.Status != ClientActivated {
		return ErrClientNotActivated
	}

	if i.Documents == nil {
		i.Documents = make(map[string]*ClientDocInfo)
	}

	hexDocID := docID.Hex()

	if i.hasDocument(hexDocID) && i.Documents[hexDocID].Status == documentAttached {
		return ErrDocumentAlreadyAttached
	}

	i.Documents[hexDocID] = &ClientDocInfo{
		Status:    documentAttached,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	i.UpdatedAt = time.Now()

	return nil
}

// DetachDocument detaches the given document from this client.
func (i *ClientInfo) DetachDocument(docID primitive.ObjectID) error {
	hexDocID := docID.Hex()
	if err := i.EnsureDocumentAttached(hexDocID); err != nil {
		return err
	}

	i.Documents[hexDocID].Status = documentDetached
	i.UpdatedAt = time.Now()

	return nil
}

// IsAttached returns whether the given document is attached to this client.
func (i *ClientInfo) IsAttached(docID primitive.ObjectID) (bool, error) {
	hexDocID := docID.Hex()

	if !i.hasDocument(hexDocID) {
		return false, ErrDocumentNeverAttached
	}

	return i.Documents[hexDocID].Status == documentAttached, nil
}

// Checkpoint returns the checkpoint of the given document.
func (i *ClientInfo) Checkpoint(docID primitive.ObjectID) *checkpoint.Checkpoint {
	clientDocInfo := i.Documents[docID.Hex()]
	if clientDocInfo == nil {
		return checkpoint.Initial
	}

	return checkpoint.New(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

// UpdateCheckpoint updates the checkpoint of the given document.
func (i *ClientInfo) UpdateCheckpoint(
	docID primitive.ObjectID,
	cp *checkpoint.Checkpoint,
) error {
	hexDocID := docID.Hex()
	if !i.hasDocument(hexDocID) {
		return ErrDocumentNeverAttached
	}

	i.Documents[hexDocID].ServerSeq = cp.ServerSeq
	i.Documents[hexDocID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

// EnsureDocumentAttached ensures the given document is attached.
func (i *ClientInfo) EnsureDocumentAttached(docID string) error {
	if i.Status != ClientActivated {
		return ErrClientNotActivated
	}

	if !i.hasDocument(docID) || i.Documents[docID].Status == documentDetached {
		return ErrDocumentNotAttached
	}

	return nil
}

func (i *ClientInfo) hasDocument(docID string) bool {
	return i.Documents != nil && i.Documents[docID] != nil
}
