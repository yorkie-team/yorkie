/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

package types

import (
	"time"

	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/resource"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// DocumentSummary represents a summary of document.
type DocumentSummary struct {
	// ID is the unique identifier of the document.
	ID ID

	// Key is the key of the document.
	Key key.Key

	// CreatedAt is the time when the document is created.
	CreatedAt time.Time

	// AccessedAt is the time when the document is accessed.
	AccessedAt time.Time

	// UpdatedAt is the time when the document is updated.
	UpdatedAt time.Time

	// Root is the root object of the document.
	Root string

	// AttachedClients is the count of clients attached to the document.
	AttachedClients int

	// DocSize represents the size of a document in bytes.
	DocSize resource.DocSize

	// SchemaKey is the key of the schema of the document.
	SchemaKey string

	// Presences is the presence information of all clients.
	Presences map[string]presence.Data
}
