/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Snapshot represents a snapshot operation.
type Snapshot struct {
	// root is the root object of this snapshot.
	root *json.Object

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewSnapshot creates a new snapshot operation.
func NewSnapshot(root *json.Object, executedAt *time.Ticket) *Snapshot {
	return &Snapshot{
		root:       root,
		executedAt: executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (o *Snapshot) Execute(root *json.Root) error {
	root.SetRootObject(o.root)
	return nil
}

// SetActor sets the actor of this operation.
func (o *Snapshot) SetActor(id time.ActorID) {
	// NOTE(hackerwins): Snapshot operations are not created locally for now.
	// Later, when we can create a snapshot operation locally, we need to handle
	// updating the actorID.
}

// ExecutedAt returns the time the operation was executed.
func (o *Snapshot) ExecutedAt() *time.Ticket {
	return o.executedAt
}

// Root returns the root object of this snapshot.
func (o *Snapshot) Root() *json.Object {
	return o.root
}
