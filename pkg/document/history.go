/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package document

import (
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// MaxUndoRedoStackDepth is the maximum depth of the undo/redo stacks. It
// mirrors the JS SDK's MaxUndoRedoStackDepth.
const MaxUndoRedoStackDepth = 50

// HistoryOperation is a single entry in an undo/redo stack. Op is nil when
// this entry carries a presence change instead of an operation.
type HistoryOperation struct {
	Op operations.Operation

	// Presence holds the values undo restores, keyed by presence key.
	Presence presence.Data

	// PresenceAbsentKeys names the presence keys the reversed change
	// introduced. They held nothing before it, so undo removes them rather
	// than setting them to the empty string. See change.Context's
	// ReversePresence.
	PresenceAbsentKeys []string
}

// History stores the undo/redo stacks of a document. Each entry is the set of
// reverse operations produced by one change.
type History struct {
	undoStack [][]HistoryOperation
	redoStack [][]HistoryOperation
}

// NewHistory creates a new instance of History.
func NewHistory() *History {
	return &History{}
}

// HasUndo reports whether there is anything to undo.
func (h *History) HasUndo() bool { return len(h.undoStack) > 0 }

// HasRedo reports whether there is anything to redo.
func (h *History) HasRedo() bool { return len(h.redoStack) > 0 }

// PushUndo pushes the reverse operations of a change onto the undo stack,
// dropping the oldest entry when the stack is full.
func (h *History) PushUndo(ops []HistoryOperation) {
	if len(h.undoStack) >= MaxUndoRedoStackDepth {
		h.undoStack = h.undoStack[1:]
	}
	h.undoStack = append(h.undoStack, ops)
}

// PopUndo pops the most recent entry off the undo stack. It returns nil when
// the stack is empty.
func (h *History) PopUndo() []HistoryOperation {
	if len(h.undoStack) == 0 {
		return nil
	}
	ops := h.undoStack[len(h.undoStack)-1]
	h.undoStack = h.undoStack[:len(h.undoStack)-1]
	return ops
}

// PushRedo pushes the reverse operations of a change onto the redo stack,
// dropping the oldest entry when the stack is full.
func (h *History) PushRedo(ops []HistoryOperation) {
	if len(h.redoStack) >= MaxUndoRedoStackDepth {
		h.redoStack = h.redoStack[1:]
	}
	h.redoStack = append(h.redoStack, ops)
}

// PopRedo pops the most recent entry off the redo stack. It returns nil when
// the stack is empty.
func (h *History) PopRedo() []HistoryOperation {
	if len(h.redoStack) == 0 {
		return nil
	}
	ops := h.redoStack[len(h.redoStack)-1]
	h.redoStack = h.redoStack[:len(h.redoStack)-1]
	return ops
}

// ClearUndo empties the undo stack.
func (h *History) ClearUndo() { h.undoStack = nil }

// ClearRedo empties the redo stack.
func (h *History) ClearRedo() { h.redoStack = nil }

// ReconcileCreatedAt rewrites any stacked operation's createdAt or
// prevCreatedAt that still points at prevCreatedAt, replacing it with
// currCreatedAt. It mirrors History.reconcileCreatedAt in the JS SDK
// (history.ts:123-160).
//
// When undo/redo replaces an element under a fresh identity -- for
// example, an Add reverse restoring a removed array element acts as an
// UndoRemove and is given a new createdAt at execution time -- any other
// stacked operation that still targets the old identity must be updated to
// the new one, or it will target a tombstone (or a since-repurposed
// identity) the next time it runs.
func (h *History) ReconcileCreatedAt(prevCreatedAt, currCreatedAt *time.Ticket) {
	replace := func(stack [][]HistoryOperation) {
		// TODO(hackerwins): Optimize by indexing operations.
		for _, entries := range stack {
			for _, entry := range entries {
				switch op := entry.Op.(type) {
				case *operations.ArraySet:
					if op.CreatedAt() != nil && op.CreatedAt().Key() == prevCreatedAt.Key() {
						op.SetCreatedAt(currCreatedAt)
					}
				case *operations.Remove:
					if op.CreatedAt() != nil && op.CreatedAt().Key() == prevCreatedAt.Key() {
						op.SetCreatedAt(currCreatedAt)
					}
				case *operations.Move:
					if op.CreatedAt() != nil && op.CreatedAt().Key() == prevCreatedAt.Key() {
						op.SetCreatedAt(currCreatedAt)
					}
					if op.PrevCreatedAt() != nil && op.PrevCreatedAt().Key() == prevCreatedAt.Key() {
						op.SetPrevCreatedAt(currCreatedAt)
					}
				case *operations.Add:
					if op.PrevCreatedAt() != nil && op.PrevCreatedAt().Key() == prevCreatedAt.Key() {
						op.SetPrevCreatedAt(currCreatedAt)
					}
				}
			}
		}
	}
	replace(h.undoStack)
	replace(h.redoStack)
}

// ReconcileTextEdit rewrites the from/to positions of any stacked Edit
// operation targeting the given parent Text so it stays correct after a
// remote edit on that Text executes. It mirrors History.reconcileTextEdit in
// the JS SDK (history.ts:162-186).
func (h *History) ReconcileTextEdit(parentCreatedAt *time.Ticket, from, to, contentLen int) {
	replace := func(stack [][]HistoryOperation) {
		// TODO(hackerwins): Optimize by indexing operations.
		for _, entries := range stack {
			for _, entry := range entries {
				if edit, ok := entry.Op.(*operations.Edit); ok &&
					edit.ParentCreatedAt().Key() == parentCreatedAt.Key() {
					edit.ReconcileOperation(from, to, contentLen)
				}
			}
		}
	}
	replace(h.undoStack)
	replace(h.redoStack)
}

// ReconcileTreeEdit rewrites the fromIdx/toIdx of any stacked TreeEdit
// operation targeting the given parent Tree so it stays correct after a
// remote edit on that Tree executes. It mirrors History.reconcileTreeEdit in
// the JS SDK (history.ts:188-215).
func (h *History) ReconcileTreeEdit(parentCreatedAt *time.Ticket, from, to, contentSize int) {
	replace := func(stack [][]HistoryOperation) {
		// TODO(hackerwins): Optimize by indexing operations.
		for _, entries := range stack {
			for _, entry := range entries {
				if edit, ok := entry.Op.(*operations.TreeEdit); ok &&
					edit.ParentCreatedAt().Key() == parentCreatedAt.Key() {
					edit.ReconcileOperation(from, to, contentSize)
				}
			}
		}
	}
	replace(h.undoStack)
	replace(h.redoStack)
}
