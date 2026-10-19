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

// Package revisions provides revisions management for document versioning.
package revisions

import (
	"context"
	"fmt"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/document/yson"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/packs"
)

// Create creates a new revision for the given document.
// Seq is auto-incremented per document.
func Create(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
	label string,
	description string,
) (*types.RevisionSummary, error) {
	// Find the document info
	docInfo, err := be.DB.FindDocInfoByRefKey(ctx, docRefKey)
	if err != nil {
		return nil, fmt.Errorf("create revision of %s: %w", docRefKey, err)
	}

	// Build the internal document at the current server sequence
	doc, err := packs.BuildInternalDocForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		return nil, fmt.Errorf("create revision of %s: %w", docRefKey, err)
	}

	// Generate snapshot from the root object (YSON format)
	ysonRoot, err := yson.FromCRDT(doc.RootObject())
	if err != nil {
		return nil, fmt.Errorf("create revision of %s: %w", docRefKey, err)
	}
	ysonObj := ysonRoot.(yson.Object)
	snapshot, err := ysonObj.Marshal()
	if err != nil {
		return nil, fmt.Errorf("create revision of %s: %w", docRefKey, err)
	}

	revision, err := be.DB.CreateRevisionInfo(
		ctx,
		docRefKey,
		label,
		description,
		[]byte(snapshot),
	)
	if err != nil {
		return nil, fmt.Errorf("create revision: %w", err)
	}

	return revision.ToTypesRevisionSummary(), nil
}

// List returns the revisions of the given document by paging.
// If includeSnapshot is false, Snapshot field will be nil for efficiency.
func List(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
	paging types.Paging[int],
	includeSnapshot bool,
) ([]*types.RevisionSummary, error) {
	revisions, err := be.DB.FindRevisionInfosByPaging(ctx, docRefKey, paging, includeSnapshot)
	if err != nil {
		return nil, fmt.Errorf("find revisions: %w", err)
	}

	var summaries []*types.RevisionSummary
	for _, rev := range revisions {
		summaries = append(summaries, rev.ToTypesRevisionSummary())
	}

	return summaries, nil
}

// Get returns a revision by its ID with full snapshot data. The ID alone names
// a revision in any project, so callers acting on behalf of a client must use
// GetForDoc instead and let the document they were authorized against bound
// what the ID can reach.
func Get(
	ctx context.Context,
	be *backend.Backend,
	revisionID types.ID,
) (*types.RevisionSummary, error) {
	revision, err := be.DB.FindRevisionInfoByID(ctx, revisionID)
	if err != nil {
		return nil, fmt.Errorf("find revision by id: %w", err)
	}

	return revision.ToTypesRevisionSummary(), nil
}

// GetForDoc returns a revision by its ID with full snapshot data, after binding
// it to the given document. A revision that belongs elsewhere is reported as
// not found, so the ID cannot be used to probe for revisions of documents the
// caller has no access to.
func GetForDoc(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
	revisionID types.ID,
) (*types.RevisionSummary, error) {
	revision, err := findForDoc(ctx, be, docRefKey, revisionID)
	if err != nil {
		return nil, err
	}

	return revision.ToTypesRevisionSummary(), nil
}

// findForDoc returns the revision with the given ID only when it belongs to the
// given document.
func findForDoc(
	ctx context.Context,
	be *backend.Backend,
	docRefKey types.DocRefKey,
	revisionID types.ID,
) (*database.RevisionInfo, error) {
	revision, err := be.DB.FindRevisionInfoByID(ctx, revisionID)
	if err != nil {
		return nil, fmt.Errorf("find revision by id: %w", err)
	}

	if revision.ProjectID != docRefKey.ProjectID || revision.DocID != docRefKey.DocID {
		return nil, fmt.Errorf("find revision %s of %s: %w", revisionID, docRefKey, database.ErrRevisionNotFound)
	}

	return revision, nil
}

// Restore restores the given document to a specific revision.
// It loads the revision snapshot and applies it as a new change through the normal CRDT merge process.
// The restoration is performed using InitialActorID to avoid conflicts with client checkpoints.
//
// docRefKey names the document to restore, and the revision must belong to it.
// The revision ID cannot select the document on its own: callers authorize the
// write — and take the document lock — against a document key they resolved
// themselves, so a revision pointing elsewhere would write past both.
func Restore(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	docRefKey types.DocRefKey,
	revisionID types.ID,
) error {
	if docRefKey.ProjectID != project.ID {
		return fmt.Errorf("restore revision of %s: %w", docRefKey, database.ErrDocumentNotFound)
	}

	// Find the revision, bound to the document being restored
	revision, err := findForDoc(ctx, be, docRefKey, revisionID)
	if err != nil {
		return err
	}

	// Find the document info
	docKey := docRefKey
	docInfo, err := be.DB.FindDocInfoByRefKey(ctx, docKey)
	if err != nil {
		return err
	}

	// Parse the snapshot YSON
	var obj yson.Object
	if err := yson.Unmarshal(string(revision.Snapshot), &obj); err != nil {
		return err
	}

	// Build document using InitialActorID
	doc, err := packs.BuildDocForCheckpoint(
		ctx,
		be,
		docInfo,
		change.Checkpoint{
			ServerSeq: docInfo.ServerSeq,
			ClientSeq: 0,
		},
		time.InitialActorID,
	)
	if err != nil {
		return err
	}

	// Update the document with the snapshot content
	if err := doc.Update(func(r *json.Object, p *presence.Presence) error {
		// Delete all existing keys
		var keys []string
		for key := range r.Object.Members() {
			keys = append(keys, key)
		}
		for _, key := range keys {
			r.Delete(key)
		}

		r.SetYSON(obj)
		return nil
	}); err != nil {
		return err
	}

	// The restored root is known in full here, so measure it against the
	// project quota directly. The snapshot-based push gate only knows the
	// document is over quota and would refuse the restore, including one that
	// brings the document back under the limit.
	if err := packs.CheckLiveSize(
		docInfo.Key,
		doc.DocSize(),
		project.MaxSizePerDocument,
	); err != nil {
		return err
	}

	// Apply the change through the normal push/pull flow using temporary client info
	if _, err := packs.PushPull(
		ctx,
		be,
		project,
		database.SystemClientInfo(docKey.ProjectID, docInfo),
		docKey,
		doc.CreateChangePack(),
		packs.PushPullOptions{
			Mode:            types.SyncModePushOnly,
			Status:          document.StatusAttached,
			DisablePresence: docInfo.DisablePresence,
			SizeChecked:     true,
		},
	); err != nil {
		return fmt.Errorf("push pull: %w", err)
	}

	return nil
}
