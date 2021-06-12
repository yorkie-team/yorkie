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

package operation

import (
	"github.com/pkg/errors"

	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Style is an operation applies the style of the given range to RichText.
type Style struct {
	// parentCreatedAt is the creation time of the RichText that executes Style.
	parentCreatedAt *time.Ticket

	// from is the starting point of the range to apply the style to.
	from *json.RGATreeSplitNodePos

	// to is the end point of the range to apply the style to.
	to *json.RGATreeSplitNodePos

	// attributes represents the text style.
	attributes map[string]string

	// executedAt is the time the operation was executed.
	executedAt *time.Ticket
}

// NewStyle creates a new instance of Style.
func NewStyle(
	parentCreatedAt *time.Ticket,
	from *json.RGATreeSplitNodePos,
	to *json.RGATreeSplitNodePos,
	attributes map[string]string,
	executedAt *time.Ticket,
) *Style {
	return &Style{
		parentCreatedAt: parentCreatedAt,
		from:            from,
		to:              to,
		attributes:      attributes,
		executedAt:      executedAt,
	}
}

// Execute executes this operation on the given document(`root`).
func (e *Style) Execute(root *json.Root) error {
	parent := root.FindByCreatedAt(e.parentCreatedAt)
	obj, ok := parent.(*json.RichText)
	if !ok {
		return errors.WithStack(ErrNotApplicableDataType)
	}

	obj.SetStyle(e.from, e.to, e.attributes, e.executedAt)
	return nil
}

// From returns the start point of the editing range.
func (e *Style) From() *json.RGATreeSplitNodePos {
	return e.from
}

// To returns the end point of the editing range.
func (e *Style) To() *json.RGATreeSplitNodePos {
	return e.to
}

// ExecutedAt returns execution time of this operation.
func (e *Style) ExecutedAt() *time.Ticket {
	return e.executedAt
}

// SetActor sets the given actor to this operation.
func (e *Style) SetActor(actorID *time.ActorID) {
	e.executedAt = e.executedAt.SetActorID(actorID)
}

// ParentCreatedAt returns the creation time of the RichText.
func (e *Style) ParentCreatedAt() *time.Ticket {
	return e.parentCreatedAt
}

// Attributes returns the attributes of this operation.
func (e *Style) Attributes() map[string]string {
	return e.attributes
}
