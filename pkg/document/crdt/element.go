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

package crdt

import (
	"errors"

	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// ErrChildNotFound is returned when the child is not found in the container.
var ErrChildNotFound = errors.New("child not found")

// Container represents Array or Object.
type Container interface {
	Element

	// Purge physically purges the given child element.
	Purge(child Element) error

	// Descendants returns all descendants of this container.
	Descendants(callback func(elem Element, parent Container) bool)

	// DeleteByCreatedAt removes the given element from this container.
	DeleteByCreatedAt(createdAt *time.Ticket, deletedAt *time.Ticket) Element
}

// GCElement represents Element which has GC.
type GCElement interface {
	Element
	removedNodesLen() int
	purgeRemovedNodesBefore(ticket *time.Ticket) (int, error)
}

// Element represents JSON element.
type Element interface {
	// Marshal returns the JSON encoding of this element.
	Marshal() string

	// DeepCopy copies itself deeply.
	DeepCopy() (Element, error)

	// CreatedAt returns the creation time of this element.
	CreatedAt() *time.Ticket

	// MovedAt returns the move time of this element.
	MovedAt() *time.Ticket

	// SetMovedAt sets the move time of this element.
	SetMovedAt(*time.Ticket)

	// RemovedAt returns the removal time of this element.
	RemovedAt() *time.Ticket

	// Remove removes this element.
	Remove(*time.Ticket) bool
}
