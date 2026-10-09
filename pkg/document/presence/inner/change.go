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

// Package inner provides the implementation of Presence.
// If the client is watching a document, the presence is shared with
// all other clients watching the same document.
package inner

import (
	"maps"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ChangeType represents the type of presence change.
type ChangeType string

const (
	// Put represents the presence is put.
	Put ChangeType = "put"

	// Clear represents the presence is cleared.
	Clear ChangeType = "clear"

	// Patch represents that only the listed top-level keys changed: Presence
	// holds the keys that were set and RemovedKeys the keys that were
	// deleted. The server folds it into a Put before storing.
	Patch ChangeType = "patch"
)

// Change represents the change of presence.
type Change struct {
	ChangeType ChangeType
	Presence   Presence

	// RemovedKeys lists the keys a Patch deletes. Unused by other types.
	RemovedKeys []string
}

// Execute applies the change to the given presences map.
func (c *Change) Execute(actorID time.ActorID, presences *Map) {
	switch c.ChangeType {
	case Clear:
		presences.Delete(actorID.String())
	case Patch:
		presences.Store(actorID.String(), c.ApplyTo(presences.Load(actorID.String())))
	default:
		presences.Store(actorID.String(), c.Presence)
	}
}

// ApplyTo returns the presence that results from applying this Patch to
// base. The base is left untouched, and a nil base is treated as empty.
func (c *Change) ApplyTo(base Presence) Presence {
	merged := make(Presence, len(base)+len(c.Presence))
	maps.Copy(merged, base)
	for _, key := range c.RemovedKeys {
		delete(merged, key)
	}
	maps.Copy(merged, c.Presence)
	return merged
}

// IsClear returns true if the change is of type Clear.
func (c *Change) IsClear() bool {
	return c.ChangeType == Clear
}
