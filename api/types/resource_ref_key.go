/*
 * Copyright 2023 The Yorkie Authors. All rights reserved.
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
	"fmt"
)

// DocRefKey represents an identifier used to reference a document.
type DocRefKey struct {
	ProjectID ID
	DocID     ID
}

// String returns the string representation of the given DocRefKey.
func (r DocRefKey) String() string {
	return fmt.Sprintf("Document (%s.%s)", r.ProjectID, r.DocID)
}

// ClientRefKey represents an identifier used to reference a client.
type ClientRefKey struct {
	ProjectID ID
	ClientID  ID
}

// String returns the string representation of the given ClientRefKey.
func (r ClientRefKey) String() string {
	return fmt.Sprintf("Client (%s.%s)", r.ProjectID, r.ClientID)
}

// SnapshotRefKey represents an identifier used to reference a snapshot.
type SnapshotRefKey struct {
	DocRefKey
	ServerSeq int64
}

// String returns the string representation of the given SnapshotRefKey.
func (r SnapshotRefKey) String() string {
	return fmt.Sprintf("Snapshot (%s.%s.%d)", r.ProjectID, r.DocID, r.ServerSeq)
}
