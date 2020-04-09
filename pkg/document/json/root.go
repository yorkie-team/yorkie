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

// Root is a structure represents the root of JSON. It has a hash table of
// all JSON elements to find a specific element when applying remote changes
// received from agent.
//
// Every element has a unique time ticket at creation, which allows us to find
// a particular element.
type Root struct {
	object                *Object
	elementMapByCreatedAt map[string]Element
}

// NewRoot creates a new instance of Root.
func NewRoot(root *Object) *Root {
	elementMap := make(map[string]Element)
	r := &Root{
		object:                root,
		elementMapByCreatedAt: elementMap,
	}

	r.RegisterElement(root)

	descendants := make(chan Element)
	go func() {
		root.Descendants(descendants)
		close(descendants)
	}()
	for descendant := range descendants {
		r.RegisterElement(descendant)
	}

	return r
}

// Object returns the root object of the JSON.
func (r *Root) Object() *Object {
	return r.object
}

// FindByCreatedAt returns the element of given creation time.
func (r *Root) FindByCreatedAt(createdAt *time.Ticket) Element {
	return r.elementMapByCreatedAt[createdAt.Key()]
}

// RegisterElement registers the given element to hash table.
func (r *Root) RegisterElement(elem Element) {
	r.elementMapByCreatedAt[elem.CreatedAt().Key()] = elem
}

// DeepCopy copies itself deeply.
func (r *Root) DeepCopy() *Root {
	return NewRoot(r.object.DeepCopy().(*Object))
}
