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

// Package innerpresence provides the implementation of Presence.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
package innerpresence

import "github.com/yorkie-team/yorkie/pkg/document/time"

// ChangeType represents the type of presence change.
type ChangeType string

const (
	// Put represents the presence is put.
	Put ChangeType = "put"

	// Clear represents the presence is cleared.
	Clear ChangeType = "clear"
)

// Change represents the change of presence.
type Change struct {
	ChangeType ChangeType
	Presence   Presence
}

// Execute applies the change to the given presences map.
func (c *Change) Execute(actorID time.ActorID, presences *Map) {
	if c.ChangeType == Clear {
		presences.Delete(actorID.String())
	} else {
		presences.Store(actorID.String(), c.Presence)
	}
}
