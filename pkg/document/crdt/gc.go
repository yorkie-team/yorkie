/*
 * Copyright 2024 The Yorkie Authors. All rights reserved.
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

import "github.com/yorkie-team/yorkie/pkg/document/time"

// GCPair is a structure that represents a pair of parent and child for garbage
// collection.
type GCPair struct {
	Parent GCParent
	Child  GCChild
}

// GCParent is an interface for the parent of the garbage collection target.
type GCParent interface {
	Purge(node GCChild) error
}

// GCChild is an interface for the child of the garbage collection target.
type GCChild interface {
	ID() string
	RemovedAt() *time.Ticket
}
