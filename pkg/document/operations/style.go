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

package operations

import (
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Style is an operation applies the style of the given range to Text.
type Style struct {
	// parentCreatedAt is the creation time of the Text that executes Style.
	parentCreatedAt *time.Ticket

	// from is the starting point of the range to apply the style to.
	from *crdt.RGATreeSplitNodePos

	// to is the end point of the range to apply the style to.
	to *crdt.RGATreeSplitNodePos

	// attributes represents the text style.
	attributes map[string]string

	// attributesToRemove represents the text style to be removed.
	attributesToRemove []string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewStyle creates a new instance of Style.
func NewStyle(
	parentCreatedAt *time.Ticket,
	from *crdt.RGATreeSplitNodePos,
	to *crdt.RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) *Style {
	return &Style{
		parentCreatedAt:    parentCreatedAt,
		from:               from,
		to:                 to,
		attributes:         attributes,
		attributesToRemove: []string{},
		executedAt:         executedAt,
	}
}

// NewStyleRemove creates a new instance of Style for removing attributes.
func NewStyleRemove(
	parentCreatedAt *time.Ticket,
	from *crdt.RGATreeSplitNodePos,
	to *crdt.RGATreeSplitNodePos,
	attributesToRemove []string,
	executedAt *time.Ticket,
) *Style {
	return &Style{
		parentCreatedAt:    parentCreatedAt,
		from:               from,
		to:                 to,
		attributes:         map[string]string{},
		attributesToRemove: attributesToRemove,
		executedAt:         executedAt,
	}
}

// Execute executes this operation on the given document(`root`). Unlike a
// single call from the JSON package (which only ever populates one of
// attributes or attributesToRemove), a reverse Style built by this method
// can carry both at once -- see toReverseOperation -- so both branches run
// independently here, mirroring style_operation.ts's execute (:125-169),
// rather than the two being mutually exclusive.
func (e *Style) Execute(root *crdt.Root, _ OpSource, versionVector time.VersionVector) (Operation, error) {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	obj, ok := parent.(*crdt.Text)
	if !ok {
		return nil, ErrNotApplicableDataType
	}

	reversePrevAttributes := make(map[string]string)
	var reverseAttrsToRemove []string

	// 01. Handle attributesToRemove (remove style attributes). RemoveStyle
	// only reports keys that existed, so every entry restores a value.
	if len(e.attributesToRemove) > 0 {
		pairs, diff, prevAttrs, err := obj.RemoveStyle(
			e.from, e.to, e.attributesToRemove, e.executedAt, versionVector,
		)
		for _, pair := range pairs {
			root.RegisterGCPair(pair)
			root.AdjustDiffForGCPair(&diff, pair)
		}
		root.Acc(diff)
		if err != nil {
			return nil, err
		}
		for _, prevAttr := range prevAttrs {
			reversePrevAttributes[prevAttr.Key] = prevAttr.Value
		}
	}

	// 02. Handle attributes (set style attributes). A key that already held
	// a value restores it; a key that did not exist is queued for removal
	// instead of being set back to the empty string.
	if len(e.attributes) > 0 {
		pairs, diff, prevAttrs, err := obj.Style(e.from, e.to, e.attributes, e.executedAt, versionVector)
		for _, pair := range pairs {
			root.RegisterGCPair(pair)
			root.AdjustDiffForGCPair(&diff, pair)
		}
		root.Acc(diff)
		if err != nil {
			return nil, err
		}
		for _, prevAttr := range prevAttrs {
			if prevAttr.Existed {
				reversePrevAttributes[prevAttr.Key] = prevAttr.Value
			} else {
				reverseAttrsToRemove = append(reverseAttrsToRemove, prevAttr.Key)
			}
		}
	}

	return e.toReverseOperation(reversePrevAttributes, reverseAttrsToRemove), nil
}

// toReverseOperation builds the operation that undoes this Style from the
// prior attribute state captured during Execute: reversePrevAttributes
// restores keys that held a value immediately before this operation ran
// (whichever branch reported them), and reverseAttrsToRemove removes keys
// the set-attributes branch added where none existed before. Ports
// style_operation.ts's reverse builder (:177-201).
func (e *Style) toReverseOperation(
	reversePrevAttributes map[string]string,
	reverseAttrsToRemove []string,
) Operation {
	if len(reversePrevAttributes) == 0 && len(reverseAttrsToRemove) == 0 {
		return nil
	}

	if len(reversePrevAttributes) > 0 && len(reverseAttrsToRemove) > 0 {
		return &Style{
			parentCreatedAt:    e.parentCreatedAt,
			from:               e.from,
			to:                 e.to,
			attributes:         reversePrevAttributes,
			attributesToRemove: reverseAttrsToRemove,
		}
	}

	if len(reverseAttrsToRemove) > 0 {
		return &Style{
			parentCreatedAt:    e.parentCreatedAt,
			from:               e.from,
			to:                 e.to,
			attributesToRemove: reverseAttrsToRemove,
		}
	}

	return &Style{
		parentCreatedAt: e.parentCreatedAt,
		from:            e.from,
		to:              e.to,
		attributes:      reversePrevAttributes,
	}
}

// From returns the start point of the editing range.
func (e *Style) From() *crdt.RGATreeSplitNodePos {
	return e.from
}

// To returns the end point of the editing range.
func (e *Style) To() *crdt.RGATreeSplitNodePos {
	return e.to
}

// ExecutedAt returns execution time of this operation.
func (e *Style) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *Style) SetActor(actorID time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// SetExecutedAt sets the given execution time to this operation.
func (e *Style) SetExecutedAt(executedAt *time.Ticket) {
	e.executedAt = executedAt
}

// ParentCreatedAt returns the creation time of the Text.
func (e *Style) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Attributes returns the attributes of this operation.
func (e *Style) Attributes() map[string]string {
	return e.attributes
}

// AttributesToRemove returns the attributes to remove.
func (e *Style) AttributesToRemove() []string {
	return e.attributesToRemove
}
