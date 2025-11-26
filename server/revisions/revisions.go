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
		return nil, fmt.Errorf("find document: %w", err)
	}

	// Build the internal document at the current server sequence
	doc, err := packs.BuildInternalDocForServerSeq(ctx, be, docInfo, docInfo.ServerSeq)
	if err != nil {
		return nil, fmt.Errorf("build document: %w", err)
	}

	// Generate snapshot from the root object (YSON format)
	snapshot := []byte(doc.RootObject().Marshal())

	revision, err := be.DB.CreateRevisionInfo(
		ctx,
		docRefKey,
		label,
		description,
		snapshot,
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

// Get returns a revision by its ID with full snapshot data.
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

// Restore restores a document to a specific revision.
// It loads the revision snapshot and applies it as a new change through the normal CRDT merge process.
// The restoration is performed using InitialActorID to avoid conflicts with client checkpoints.
func Restore(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	revisionID types.ID,
) error {
	// Find the revision
	revision, err := be.DB.FindRevisionInfoByID(ctx, revisionID)
	if err != nil {
		return err
	}

	if revision.ProjectID != project.ID {
		return fmt.Errorf("restore revision of %s: %w", revisionID, database.ErrRevisionNotFound)
	}

	// Find the document info
	docKey := types.DocRefKey{ProjectID: revision.ProjectID, DocID: revision.DocID}
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

	// Apply the change through the normal push/pull flow using temporary client info
	if _, err := packs.PushPull(
		ctx,
		be,
		&types.Project{ID: docKey.ProjectID},
		database.SystemClientInfo(docKey.ProjectID, docInfo),
		docKey,
		doc.CreateChangePack(),
		packs.PushPullOptions{
			Mode:   types.SyncModePushOnly,
			Status: document.StatusAttached,
		},
	); err != nil {
		return fmt.Errorf("push pull: %w", err)
	}

	return nil
}
