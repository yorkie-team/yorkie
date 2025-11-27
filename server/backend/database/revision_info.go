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

package database

import (
	"slices"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// RevisionInfo represents a document revision for version management.
// It stores a snapshot of document content at a specific point in time,
// enabling features like rollback, audit, and version history tracking.
type RevisionInfo struct {
	// ID is the unique identifier of the revision.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project that this revision belongs to.
	ProjectID types.ID `bson:"project_id"`

	// DocID is the ID of the document that this revision is for.
	DocID types.ID `bson:"doc_id"`

	// Seq is the sequence number of the revision for ordering.
	Seq int64 `bson:"seq"`

	// Label is a user-friendly name for this revision.
	Label string `bson:"label"`

	// Description is a detailed explanation of this revision.
	Description string `bson:"description"`

	// Snapshot is the serialized document content (YSON format) at this revision point.
	// This field may be nil when listing revisions (for efficiency).
	Snapshot []byte `bson:"snapshot"`

	// CreatedAt is the time when this revision was created.
	CreatedAt time.Time `bson:"created_at"`
}

// ToTypesRevisionSummary converts database.RevisionSummary to types.RevisionSummary.
func (r *RevisionInfo) ToTypesRevisionSummary() *types.RevisionSummary {
	return &types.RevisionSummary{
		ID:          r.ID,
		Seq:         r.Seq,
		Label:       r.Label,
		Description: r.Description,
		Snapshot:    string(r.Snapshot),
		CreatedAt:   r.CreatedAt,
	}
}

// DeepCopy creates a deep copy of the RevisionInfo.
func (r *RevisionInfo) DeepCopy() *RevisionInfo {
	if r == nil {
		return nil
	}

	return &RevisionInfo{
		ID:          r.ID,
		ProjectID:   r.ProjectID,
		DocID:       r.DocID,
		Seq:         r.Seq,
		Label:       r.Label,
		Description: r.Description,
		Snapshot:    slices.Clone(r.Snapshot),
		CreatedAt:   r.CreatedAt,
	}
}
