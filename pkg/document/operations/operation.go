/*
 * Copyright 2020 The Yorkie Authors. All rights reserved.
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

// Package operations implements the operations that can be executed on the
// document.
package operations

import (
	"errors"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

var (
	// ErrNotApplicableDataType occurs when attempting to execute an operation
	// on a data type that cannot be executed.
	ErrNotApplicableDataType = errors.New("not applicable datatype")

	// ErrUnknownRestoreIdentity occurs when a restore/retombstone span carries
	// a node identity the acting change could not causally have observed. It
	// guards the server-executed restore path against forged identities.
	ErrUnknownRestoreIdentity = errors.New("restore span identity is not causally known")
)

// OpSource represents the source of an operation execution. Some operations
// behave differently under undo/redo, where a Set or an Add acts as a
// replacement rather than an insertion.
type OpSource int

const (
	// OpSourceLocal is an operation executed by a local edit.
	OpSourceLocal OpSource = iota

	// OpSourceRemote is an operation received from another client.
	OpSourceRemote

	// OpSourceUndoRedo is an operation replayed from the undo/redo stack.
	OpSourceUndoRedo
)

// Operation represents an operation to be executed on a document.
type Operation interface {
	// Execute executes this operation on the given document(`root`) and
	// returns the reverse operation that undoes it. The reverse is nil when
	// this operation has none.
	Execute(root *crdt.Root, source OpSource, versionVector time.VersionVector) (Operation, error)

	// ExecutedAt returns execution time of this operation.
	ExecutedAt() *time.Ticket

	// SetActor sets the given actor to this operation.
	SetActor(id time.ActorID)

	// SetExecutedAt sets the given execution time to this operation.
	SetExecutedAt(executedAt *time.Ticket)

	// ParentCreatedAt returns the creation time of the target element to
	// execute the operation.
	ParentCreatedAt() *time.Ticket
}
