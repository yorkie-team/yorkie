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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TreeEdit is an operation representing Tree editing.
type TreeEdit struct {
	// parentCreatedAt is the creation time of the Tree that executes
	// TreeEdit.
	parentCreatedAt *time.Ticket

	// fromPos represents the start point of the editing range.
	from *crdt.TreePos

	// toPos represents the end point of the editing range.
	to *crdt.TreePos

	// contents is the content of tree added when editing.
	contents []*crdt.TreeNode

	// splitLevel is the level of the split.
	splitLevel int

	// maxCreatedAtMapByActor is a map that stores the latest creation time
	// by actor for the nodes included in the editing range.
	maxCreatedAtMapByActor map[string]*time.Ticket

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewTreeEdit creates a new instance of TreeEdit.
func NewTreeEdit(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	contents []*crdt.TreeNode,
	splitLevel int,
	maxCreatedAtMapByActor map[string]*time.Ticket,
	executedAt *time.Ticket,
) *TreeEdit {
	return &TreeEdit{
		parentCreatedAt:        parentCreatedAt,
		from:                   from,
		to:                     to,
		contents:               contents,
		splitLevel:             splitLevel,
		maxCreatedAtMapByActor: maxCreatedAtMapByActor,
		executedAt:             executedAt,
	}
}

// Execute executes this operation on the given `CRDTRoot`.
func (e *TreeEdit) Execute(root *crdt.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)

	switch obj := parent.(type) {
	case *crdt.Tree:
		var contents []*crdt.TreeNode
		var err error
		if len(e.Contents()) != 0 {
			for _, content := range e.Contents() {
				var clone *crdt.TreeNode

				clone, err = content.DeepCopy()
				if err != nil {
					return err
				}

				contents = append(contents, clone)
			}

		}
		_, pairs, err := obj.Edit(
			e.from,
			e.to,
			contents,
			e.splitLevel,
			e.executedAt,
			/**
			 * TODO(sejongk): When splitting element nodes, a new nodeID is assigned with a different timeTicket.
			 * In the same change context, the timeTickets share the same lamport and actorID but have different delimiters,
			 * incremented by one for each.
			 * Therefore, it is possible to simulate later timeTickets using `editedAt` and the length of `contents`.
			 * This logic might be unclear; consider refactoring for multi-level concurrent editing in the Tree implementation.
			 */
			func() func() *time.Ticket {
				delimiter := e.executedAt.Delimiter()
				if contents != nil {
					delimiter += uint32(len(contents))
				}
				return func() *time.Ticket {
					delimiter++
					return time.NewTicket(
						e.executedAt.Lamport(),
						delimiter,
						e.executedAt.ActorID(),
					)
				}
			}(),
			e.maxCreatedAtMapByActor,
		)
		if err != nil {
			return err
		}

		for _, pair := range pairs {
			root.RegisterGCPair(pair)

		}

	default:
		return ErrNotApplicableDataType
	}

	return nil
}

// FromPos returns the start point of the editing range.
func (e *TreeEdit) FromPos() *crdt.TreePos {
	return e.from
}

// ToPos returns the end point of the editing range.
func (e *TreeEdit) ToPos() *crdt.TreePos {
	return e.to
}

// SetActor sets the given actor to this operation.
func (e *TreeEdit) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the Text.
func (e *TreeEdit) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Contents returns the content of Edit.
func (e *TreeEdit) Contents() []*crdt.TreeNode {
	return e.contents
}

// SplitLevel returns the level of the split.
func (e *TreeEdit) SplitLevel() int {
	return e.splitLevel
}

// MaxCreatedAtMapByActor returns the map that stores the latest creation time
// by actor for the nodes included in the editing range.
func (e *TreeEdit) MaxCreatedAtMapByActor() map[string]*time.Ticket {
	return e.maxCreatedAtMapByActor
}

// ExecutedAt returns execution time of this operation.
func (e *TreeEdit) ExecutedAt() *time.Ticket {
	return e.executedAt
}
