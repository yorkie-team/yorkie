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

var (
	ErrClientNotActivated      = errors.New("client not activated")
	ErrDocumentNotAttached     = errors.New("document not attached")
	ErrDocumentNeverAttached   = errors.New("client has never attached the document")
	ErrDocumentAlreadyAttached = errors.New("document already attached")
)

const (
	ClientDeactivated = "deactivated"
	ClientActivated   = "activated"
)

const (
	DocumentAttached = "attached"
	DocumentDetached = "detached"
)

type ClientDocInfo struct {
	Status    string `bson:"status"`
	ServerSeq uint64 `bson:"server_seq"`
	ClientSeq uint32 `bson:"client_seq"`
}

type ClientInfo struct {
	ID        primitive.ObjectID        `bson:"_id"`
	Key       string                    `bson:"key"`
	Status    string                    `bson:"status"`
	Documents map[string]*ClientDocInfo `bson:"documents"`
	CreatedAt time.Time                 `bson:"created_at"`
	UpdatedAt time.Time                 `bson:"updated_at"`
}

func (i *ClientInfo) AttachDocument(docID primitive.ObjectID) error {
	if i.Status != ClientActivated {
		return ErrClientNotActivated
	}

	if i.Documents == nil {
		i.Documents = make(map[string]*ClientDocInfo)
	}

	hexDocID := docID.Hex()

	if i.hasDocument(hexDocID) {
		return ErrDocumentAlreadyAttached
	}

	i.Documents[hexDocID] = &ClientDocInfo{
		Status:    DocumentAttached,
		ServerSeq: 0,
		ClientSeq: 0,
	}
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) DetachDocument(docID primitive.ObjectID) error {
	hexDocID := docID.Hex()
	if err := i.CheckDocumentAttached(hexDocID); err != nil {
		return err
	}

	i.Documents[hexDocID].Status = DocumentDetached
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) GetCheckpoint(id primitive.ObjectID) *checkpoint.Checkpoint {
	clientDocInfo := i.Documents[id.Hex()]
	if clientDocInfo == nil {
		return checkpoint.Initial
	}

	return checkpoint.New(clientDocInfo.ServerSeq, clientDocInfo.ClientSeq)
}

func (i *ClientInfo) UpdateCheckpoint(docID primitive.ObjectID, cp *checkpoint.Checkpoint) error {
	hexDocID := docID.Hex()
	if !i.hasDocument(hexDocID) {
		return ErrDocumentNeverAttached
	}

	i.Documents[hexDocID].ServerSeq = cp.ServerSeq
	i.Documents[hexDocID].ClientSeq = cp.ClientSeq
	i.UpdatedAt = time.Now()

	return nil
}

func (i *ClientInfo) CheckDocumentAttached(hexDocID string) error {
	if i.Status != ClientActivated {
		return ErrClientNotActivated
	}

	if !i.hasDocument(hexDocID) || i.Documents[hexDocID].Status == DocumentDetached {
		return ErrDocumentNotAttached
	}

	return nil
}

func (i *ClientInfo) hasDocument(hexDocID string) bool {
	return i.Documents != nil && i.Documents[hexDocID] != nil
}
