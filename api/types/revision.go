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

package types

import (
	"time"
)

// RevisionSummary represents a document revision for version management.
// It stores a snapshot of document content at a specific point in time,
// enabling features like rollback, audit, and version history tracking.
// The Snapshot field can be omitted when listing multiple revisions for efficiency.
type RevisionSummary struct {
	// ID is the unique identifier of the revision.
	ID ID

	// Label is a user-friendly name for this revision.
	Label string

	// Description is a detailed explanation of this revision.
	Description string

	// Snapshot is the serialized document content (YSON format) at this revision point.
	// This contains only the pure data without CRDT metadata.
	Snapshot string

	// CreatedAt is the time when this revision was created.
	CreatedAt time.Time
}
