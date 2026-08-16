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
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TreeStyle represents the style operation of the tree.
type TreeStyle struct {
	// parentCreatedAt is the creation time of the Text that executes Style.
	parentCreatedAt *time.Ticket

	// fromPos represents the start point of the editing range.
	from *crdt.TreePos

	// toPos represents the end point of the editing range.
	to *crdt.TreePos

	// attributes represents the tree style to be added.
	attributes map[string]string

	// attributesToRemove represents the tree style to be removed.
	attributesToRemove []string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewTreeStyle creates a new instance of TreeStyle.
func NewTreeStyle(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) *TreeStyle {
	return &TreeStyle{
		parentCreatedAt:    parentCreatedAt,
		from:               from,
		to:                 to,
		attributes:         attributes,
		attributesToRemove: []string{},
		executedAt:         executedAt,
	}
}

// NewTreeStyleRemove creates a new instance of TreeStyle.
func NewTreeStyleRemove(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	attributesToRemove []string,
	executedAt *time.Ticket,
) *TreeStyle {
	return &TreeStyle{
		parentCreatedAt:    parentCreatedAt,
		from:               from,
		to:                 to,
		attributes:         map[string]string{},
		attributesToRemove: attributesToRemove,
		executedAt:         executedAt,
	}
}

// NewTreeStyleSetAndRemove creates a TreeStyle operation that both sets
// attributes and removes others in the same call. NewTreeStyle and
// NewTreeStyleRemove each zero out the field the other populates, so this
// constructor exists for the shape a reverse can need: restoring some keys
// and removing others that did not exist before, in a single op. Wire
// decoding must use this whenever a decoded TreeStyle carries both fields
// non-empty, matching JS's TreeStyleOperation constructor, which always
// accepts both -- decoding the two fields exclusively would silently drop
// whichever field lost.
func NewTreeStyleSetAndRemove(
	parentCreatedAt *time.Ticket,
	from *crdt.TreePos,
	to *crdt.TreePos,
	attributes map[string]string,
	attributesToRemove []string,
	executedAt *time.Ticket,
) *TreeStyle {
	return &TreeStyle{
		parentCreatedAt:    parentCreatedAt,
		from:               from,
		to:                 to,
		attributes:         attributes,
		attributesToRemove: attributesToRemove,
		executedAt:         executedAt,
	}
}

// Execute executes this operation on the given document(`root`). Ports
// tree_style_operation.ts's execute (:127-158) exactly, including its
// if/else shape: unlike Style.Execute (the Text twin), which runs its
// attributes and attributesToRemove branches independently, TreeStyle
// only ever runs one branch per call, attributes taking priority. A
// forward call from the JSON package is always single-branch already (see
// json/tree.go's Tree.Style and Tree.RemoveStyle), so this only matters
// when Execute runs a combined reverse (built by toReverseOperation) that
// carries both fields -- and there, matching JS, the attributesToRemove
// half is not applied. This is a known JS defect (PR #1221 copied Text's
// combined-reverse constructor without also copying Text's independent-if
// execute shape from PR #1174), preserved here rather than fixed --
// see docs/tasks/active/20260816-remote-redo-replica-divergence-todo.md.
func (e *TreeStyle) Execute(root *crdt.Root, _ OpSource, versionVector time.VersionVector) (ExecutionResult, error) {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	obj, ok := parent.(*crdt.Tree)
	if !ok {
		return ExecutionResult{}, ErrNotApplicableDataType
	}

	reversePrevAttributes := make(map[string]string)
	var reverseAttrsToRemove []string

	var pairs []crdt.GCPair
	var diff resource.DataSize
	var err error
	if len(e.attributes) > 0 {
		// A key that already held a value restores it; a key that did not
		// exist is queued for removal instead of being set back to the
		// empty string.
		var prevAttrs []crdt.PrevAttr
		pairs, diff, prevAttrs, err = obj.Style(e.from, e.to, e.attributes, e.executedAt, versionVector)
		for _, prevAttr := range prevAttrs {
			if prevAttr.Existed {
				reversePrevAttributes[prevAttr.Key] = prevAttr.Value
			} else {
				reverseAttrsToRemove = append(reverseAttrsToRemove, prevAttr.Key)
			}
		}
	} else {
		// RemoveStyle only reports keys that existed, so every entry
		// restores a value.
		var prevAttrs []crdt.PrevAttr
		pairs, diff, prevAttrs, err = obj.RemoveStyle(
			e.from, e.to, e.attributesToRemove, e.executedAt, versionVector,
		)
		for _, prevAttr := range prevAttrs {
			reversePrevAttributes[prevAttr.Key] = prevAttr.Value
		}
	}

	for _, pair := range pairs {
		root.RegisterGCPair(pair)
		root.AdjustDiffForGCPair(&diff, pair)
	}
	root.Acc(diff)
	if err != nil {
		return ExecutionResult{}, err
	}

	// JS derives this operation's OpInfos from the change list tree.style
	// and tree.removeStyle return (tree_style_operation.ts:196), which is
	// empty only when the range covered no styleable node. Tree.Style does
	// not report that list, so this stays conservative: see
	// ExecutionResult.Observable on why an operation that cannot decide
	// reports true.
	return ExecutionResult{
		Reverse:    e.toReverseOperation(reversePrevAttributes, reverseAttrsToRemove),
		Observable: true,
	}, nil
}

// toReverseOperation builds the operation that undoes this TreeStyle from
// the prior attribute state captured during Execute: reversePrevAttributes
// restores keys that held a value immediately before this operation ran
// (whichever branch reported them), and reverseAttrsToRemove removes keys
// the set-attributes branch added where none existed before. Ports
// tree_style_operation.ts's reverse builder (:166-193).
func (e *TreeStyle) toReverseOperation(
	reversePrevAttributes map[string]string,
	reverseAttrsToRemove []string,
) Operation {
	if len(reversePrevAttributes) == 0 && len(reverseAttrsToRemove) == 0 {
		return nil
	}

	if len(reversePrevAttributes) > 0 && len(reverseAttrsToRemove) > 0 {
		return NewTreeStyleSetAndRemove(
			e.parentCreatedAt, e.from, e.to, reversePrevAttributes, reverseAttrsToRemove, nil,
		)
	}

	if len(reverseAttrsToRemove) > 0 {
		return NewTreeStyleRemove(e.parentCreatedAt, e.from, e.to, reverseAttrsToRemove, nil)
	}

	return NewTreeStyle(e.parentCreatedAt, e.from, e.to, reversePrevAttributes, nil)
}

// FromPos returns the start point of the editing range.
func (e *TreeStyle) FromPos() *crdt.TreePos {
	return e.from
}

// ToPos returns the end point of the editing range.
func (e *TreeStyle) ToPos() *crdt.TreePos {
	return e.to
}

// ExecutedAt returns execution time of this operation.
func (e *TreeStyle) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *TreeStyle) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *TreeStyle) SetExecutedAt(executedAt *time.Ticket) {
	e.executedAt = executedAt
}

// ParentCreatedAt returns the creation time of the Text.
func (e *TreeStyle) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Attributes returns the content of Style.
func (e *TreeStyle) Attributes() map[string]string {
	return e.attributes
}

// AttributesToRemove returns the content of Style.
func (e *TreeStyle) AttributesToRemove() []string {
	return e.attributesToRemove
}
