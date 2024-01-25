/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"time"

	"github.com/yorkie-team/yorkie/api/types"
)

// SnapshotInfo is a structure representing information of the snapshot.
type SnapshotInfo struct {
	// ID is the unique ID of the snapshot.
	ID types.ID `bson:"_id"`

	// ProjectID is the ID of the project which the snapshot belongs to.
	ProjectID types.ID `bson:"project_id"`

	// DocID is the ID of the document which the snapshot belongs to.
	DocID types.ID `bson:"doc_id"`

	// ServerSeq is the sequence number of the server which the snapshot belongs to.
	ServerSeq int64 `bson:"server_seq"`

	// Lamport is the Lamport timestamp of the snapshot.
	Lamport int64 `bson:"lamport"`

	// Snapshot is the snapshot data.
	Snapshot []byte `bson:"snapshot"`

	// CreatedAt is the time when the snapshot is created.
	CreatedAt time.Time `bson:"created_at"`
}

// DeepCopy returns a deep copy of the SnapshotInfo.
func (i *SnapshotInfo) DeepCopy() *SnapshotInfo {
	if i == nil {
		return nil
	}

	return &SnapshotInfo{
		ID:        i.ID,
		ProjectID: i.ProjectID,
		DocID:     i.DocID,
		ServerSeq: i.ServerSeq,
		Lamport:   i.Lamport,
		Snapshot:  i.Snapshot,
		CreatedAt: i.CreatedAt,
	}
}

// RefKey returns the refKey of the snapshot.
func (i *SnapshotInfo) RefKey() types.SnapshotRefKey {
	return types.SnapshotRefKey{
		DocRefKey: types.DocRefKey{
			ProjectID: i.ProjectID,
			DocID:     i.DocID,
		},
		ServerSeq: i.ServerSeq,
	}
}
