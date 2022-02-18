/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package packs

import (
	"context"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/yorkie/backend"
	"github.com/yorkie-team/yorkie/yorkie/backend/db"
)

// FindAllChanges fetches all changes of the given document.
func FindAllChanges(
	ctx context.Context,
	be *backend.Backend,
	docInfo *db.DocInfo,
) ([]*change.Change, error) {
	changes, err := be.DB.FindChangesBetweenServerSeqs(
		ctx,
		docInfo.ID,
		0,
		docInfo.ServerSeq,
	)
	return changes, err
}

// CreateChangePackByServerSeq creates a change pack by the given server seq.
func CreateChangePackByServerSeq(
	ctx context.Context,
	be *backend.Backend,
	clientInfo *db.ClientInfo,
	docInfo *db.DocInfo,
	serverSeq uint64,
	message string,
) (*change.Pack, error) {
	actorID, err := clientInfo.ID.ToActorID()
	if err != nil {
		return nil, err
	}

	// 01. build the document for the given server seq.
	sourceDoc, err := buildDocForServerSeq(ctx, be, docInfo, serverSeq)
	if err != nil {
		return nil, err
	}

	// 02. rebuild the document for latest server seq.
	targetInternalDoc, err := buildDocForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		return nil, err
	}
	targetDoc := document.NewFromInternalDocument(targetInternalDoc, actorID)

	// 03. replace contents of the target document with the source document.
	if err = targetDoc.Replace(sourceDoc.Root().Object(), message); err != nil {
		return nil, err
	}

	return targetDoc.CreateChangePack(), nil
}
