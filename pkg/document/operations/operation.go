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

	// ErrOperationSkipped signals that an operation declined to execute
	// because its target no longer exists -- a concurrently removed element
	// during undo/redo. It is not a failure: the caller must drop the
	// operation and keep going, exactly as JS's Change.execute does with an
	// undefined execution result (change.ts:174).
	//
	// A skipped operation must not appear in Change.Execute's executed list.
	// The distinction matters because "executed with no reverse" and
	// "skipped" are otherwise indistinguishable when both return a nil
	// reverse, and reporting a skip as executed makes a fully skipped undo
	// look like a real change -- one that peers would then apply under
	// OpSourceRemote, where the skip guard does not run.
	ErrOperationSkipped = errors.New("operation skipped")
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

	// OpSourceReplay is a stored change replayed into a document whose caller
	// keeps nothing the execution reports: the server rebuilding a document
	// from its change log (InternalDocument.ApplyChangePack, reached from
	// BuildInternalDocForServerSeq on every snapshot, compaction and
	// cache-missing push-pull). It resolves conflicts exactly as
	// OpSourceRemote does; the only difference is that bookkeeping which only
	// an undo/redo history reads -- the reverse operation, and the pre-edit
	// visible-index range TreeEdit.NormalizePos reports -- is skipped rather
	// than computed and thrown away, which is what keeps the replay linear in
	// the change count.
	//
	// A client applying remote changes must NOT use this: Document.applyChanges
	// reconciles its stacked undo/redo entries against exactly that index
	// range, so it keeps OpSourceRemote.
	OpSourceReplay
)

// NeedsReverse reports whether an operation executed from this source has to
// build the reverse operation that undoes it. Only a local edit and an
// undo/redo replay can themselves be undone; both remote sources discard the
// reverse, so building one is pure cost.
func (s OpSource) NeedsReverse() bool {
	return s == OpSourceLocal || s == OpSourceUndoRedo
}

// isRemovedOrOrphaned reports whether elem, or any of its ancestors up to
// the document root, has been removed. During undo/redo an operation
// targeting such an element is skipped rather than executed, mirroring
// set_operation.ts:81-89 and remove_operation.ts:84-92.
//
// The Go Root does not maintain a live parent index the way the JS SDK's
// elementPairMapByCreatedAt does (it only tracks parents for elements
// already marked removed). The ancestor chain is instead recovered by
// walking the document tree from the root once per call. This only runs on
// the undo/redo path, not on every local or remote edit.
//
// The walk is O(document size) where JS's lookup is O(1), and an undo entry
// of N operations pays it 2N times (once against the clone, once against the
// real root). Two ways out were considered and both rejected for now:
//
//   - Caching the map per Change.Execute call is not sound. The operations
//     in one entry mutate the very structure the map describes -- a Set that
//     restores an element changes what the next operation's ancestor walk
//     should find -- so a map built once per change can answer a later
//     operation with pre-change parentage.
//   - Maintaining a live parent index means Root.RegisterElement taking the
//     parent, as JS's registerElement does, and every call site supplying
//     it. That is a structural change to Root, and it overlaps the
//     DeregisterElement accounting already filed in
//     docs/tasks/archive/2026/08/20260816-root-docsize-nested-container-gc-todo.md,
//     so it belongs with that decision rather than ahead of it.
func isRemovedOrOrphaned(root *crdt.Root, elem crdt.Element) bool {
	if elem == nil {
		return false
	}

	parents := make(map[string]crdt.Container)
	root.Object().Descendants(func(child crdt.Element, parent crdt.Container) bool {
		parents[child.CreatedAt().Key()] = parent
		return false
	})

	for elem != nil {
		if elem.RemovedAt() != nil {
			return true
		}
		parent, ok := parents[elem.CreatedAt().Key()]
		if !ok {
			return false
		}
		elem = parent
	}
	return false
}

// ExecutionResult is what an operation reports after executing. It is the
// port of the JS SDK's ExecutionResult (operation.ts:190-193), minus the
// OpInfo list Go does not materialize.
type ExecutionResult struct {
	// Reverse is the operation that undoes this execution. It is nil when
	// the operation has no reverse, and also when the source discards it
	// (see OpSource.NeedsReverse).
	Reverse Operation

	// Observable reports whether this execution changed anything a peer or
	// an editor binding would see. It stands in for JS's `opInfos.length >
	// 0` (change.ts:176): JS emits one OpInfo per observable effect, and
	// Document gates both the redo-stack clear (document.ts:768) and the
	// undo/redo propagation (document.ts:2145) on that list being non-empty.
	//
	// Reporting true when JS would emit nothing costs a redundant change on
	// the wire; reporting false when JS would emit something silently drops
	// a real edit. So an operation that cannot decide reports true, which is
	// what Go did for every operation before this field existed. Today only
	// Edit decides: its JS counterpart derives OpInfos from the change list
	// text.edit returns, which is non-empty exactly when content was
	// inserted (rga_tree_split.ts:665) or a live node was removed
	// (rga_tree_split.ts:1564). Style, TreeEdit and TreeStyle derive their
	// OpInfos the same way in JS, but the Go CRDT layer does not report the
	// per-node change list they would need, so they stay conservative.
	Observable bool
}

// Operation represents an operation to be executed on a document.
type Operation interface {
	// Execute executes this operation on the given document(`root`) and
	// reports the reverse operation that undoes it, along with whether the
	// execution was observable. See ExecutionResult.
	//
	// An implementation that cannot apply because its target no longer
	// exists must return ErrOperationSkipped instead of a nil error. That
	// binds every implementer: Change.Execute treats ErrOperationSkipped as
	// "skipped, not applied" and excludes the operation from both the
	// executed list and the reverse operations it returns, rather than
	// treating it as a no-op execution.
	Execute(root *crdt.Root, source OpSource, versionVector time.VersionVector) (ExecutionResult, error)

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
