/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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

// Package attachable provides common interface for attachable resources.
package attachable

import (
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// ResourceType represents the type of attachable resource.
type ResourceType string

const (
	// TypeDocument represents a document resource.
	TypeDocument ResourceType = "document"

	// TypeChannel represents a channel resource.
	TypeChannel ResourceType = "channel"
)

// StatusType represents the status of the attachable resource.
type StatusType int

const (
	// StatusDetached means that the resource is not attached to the client.
	StatusDetached StatusType = iota

	// StatusAttached means that this resource is attached to the client.
	StatusAttached

	// StatusRemoved means that this resource is removed.
	StatusRemoved
)

// Attachable represents a resource that can be attached to a client.
type Attachable interface {
	// Key returns the key of this resource.
	Key() key.Key

	// Type returns the type of this resource.
	Type() ResourceType

	// Status returns the status of this resource.
	Status() StatusType

	// SetStatus updates the status of this resource.
	SetStatus(StatusType)

	// IsAttached returns whether this resource is attached or not.
	IsAttached() bool

	// ActorID returns ID of the actor currently editing the resource.
	ActorID() time.ActorID

	// SetActor sets actor into this resource.
	SetActor(time.ActorID)
}
