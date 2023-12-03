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
	"errors"
	"fmt"
	"strings"

	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// ErrInvalidDocRefKeyStringFormat is returned when the input of DocRefKey Set is invalid.
var ErrInvalidDocRefKeyStringFormat = errors.New("use the format 'docKey,docID' for the string of the docRefKey")

// DocRefKey represents an identifier used to reference a document.
type DocRefKey struct {
	Key key.Key
	ID  ID
}

// String returns the string representation of the given DocRefKey.
func (r DocRefKey) String() string {
	return fmt.Sprintf("Document (%s.%s)", r.Key, r.ID)
}

// Set parses the given string (format: `{docKey},{docID}`) and assigns the values
// to the given DocRefKey.
func (r *DocRefKey) Set(v string) error {
	parsed := strings.Split(v, ",")
	if len(parsed) != 2 {
		return ErrInvalidDocRefKeyStringFormat
	}
	r.Key = key.Key(parsed[0])
	r.ID = ID(parsed[1])
	return nil
}

// Type returns the type string of the given DocRefKey, used in cli help text.
func (r DocRefKey) Type() string {
	return "DocumentRefKey"
}

// SnapshotRefKey represents an identifier used to reference a snapshot.
type SnapshotRefKey struct {
	DocRefKey
	ServerSeq int64
}

// String returns the string representation of the given SnapshotRefKey.
func (r SnapshotRefKey) String() string {
	return fmt.Sprintf("Snapshot (%s.%s.%d)", r.DocRefKey.Key, r.DocRefKey.ID, r.ServerSeq)
}
