/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package packs implements PushPullPack which is used to sync the document
// between the client and the server.
package packs

import (
	"context"
	"errors"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
)

var (
	// ErrDocumentNotRemoved is returned when the document is not removed yet.
	ErrDocumentNotRemoved = errors.New("document is not removed yet")
)

// Compact compacts the given document and its metadata and stores them in the
// database.
func Compact(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	docInfo *database.DocInfo,
) error {
	// 1. Check if the document is attached.
	isAttached, err := be.DB.IsDocumentAttached(ctx, types.DocRefKey{
		ProjectID: projectID,
		DocID:     docInfo.ID,
	}, "")
	if err != nil {
		return err
	}
	if isAttached {
		// TODO(hackerwins): ErrDocumentNotAttached exists in documents package,
		// but it can not be here because of the circular dependency.
		return fmt.Errorf("document is attached")
	}

	// 2. Build compacted changes and check if the content is the same.
	doc, err := BuildInternalDocForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		logging.DefaultLogger().Errorf("[CD] Document %s failed to apply changes: %v\n", docInfo.ID, err)
		return err
	}

	root, err := yson.FromCRDT(doc.RootObject())
	if err != nil {
		return err
	}

	newDoc := document.New(docInfo.Key)
	if err = newDoc.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetYSON(root)
		return nil
	}); err != nil {
		return err
	}

	newRoot, err := yson.FromCRDT(newDoc.RootObject())
	if err != nil {
		return err
	}

	// 3. Check if the content is the same after rebuilding.
	prevMarshalled, err := root.(yson.Object).Marshal()
	if err != nil {
		return err
	}
	newMarshalled, err := newRoot.(yson.Object).Marshal()
	if err != nil {
		return err
	}
	if prevMarshalled != newMarshalled {
		return fmt.Errorf("content mismatch after rebuild: %s", docInfo.ID)
	}

	// 4. Invalidate snapshot cache.
	be.Cache.Snapshot.Remove(docInfo.RefKey())

	// 5. Store compacted changes and metadata in the database.
	if err = be.DB.CompactChangeInfos(
		ctx,
		docInfo,
		docInfo.ServerSeq,
		newDoc.CreateChangePack().Changes,
	); err != nil {
		logging.DefaultLogger().Errorf(
			"[CD] Document %s failed to compact: %v\n Root: %s\n",
			docInfo.ID, err, prevMarshalled,
		)
		return err
	}

	return nil
}

// Purge removes the document from the database permanently.
func Purge(
	ctx context.Context,
	be *backend.Backend,
	projectID types.ID,
	docInfo *database.DocInfo,
) error {
	// 0. Invalidate snapshot cache.
	be.Cache.Snapshot.Remove(docInfo.RefKey())

	// 1. Check if the document is removed.
	if !docInfo.IsRemoved() {
		return fmt.Errorf("document %s is not removed: %w", docInfo.ID, ErrDocumentNotRemoved)
	}

	// 2. Purge the document from the database.
	counts, err := be.DB.PurgeDocument(ctx, docInfo.RefKey())
	if err != nil {
		return err
	}

	logging.From(ctx).Infow(fmt.Sprintf(
		"purged document internals [project_id=%s doc_id=%s]",
		projectID, docInfo.ID,
	), "changes", counts["changes"], "snapshots", counts["snapshots"], "versionvectors", counts["versionvectors"])

	return nil
}
