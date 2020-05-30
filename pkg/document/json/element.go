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

package json

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Container represents Array or Object.
type Container interface {
	Element
	Purge(child Element)
	Descendants(callback func(elem Element, parent Container) bool)
}

// Element represents JSON element.
type Element interface {
	// Marshal returns the JSON encoding of this element.
	Marshal() string

	// DeepCopy copies itself deeply.
	DeepCopy() Element

	// CreatedAt returns the creation time of this element.
	CreatedAt() *time.Ticket

	// UpdatedAt returns the update time of this element.
	UpdatedAt() *time.Ticket

	// SetUpdatedAt sets the update time of this element.
	SetUpdatedAt(*time.Ticket)

	// RemovedAt returns the removal time of this element.
	RemovedAt() *time.Ticket

	// Remove removes this element.
	Remove(*time.Ticket) bool
}
