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

package change

import (
	"github.com/yorkie-team/yorkie/pkg/document/key"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// Pack is a unit for delivering changes in a document to the remote.
type Pack struct {
	// DocumentKey is key of the document.
	DocumentKey key.Key

	// Checkpoint is used to determine the client received changes.
	Checkpoint Checkpoint

	// Change represents a unit of modification in the document.
	Changes []*Change

	// Snapshot is a byte array that encode the document.
	Snapshot []byte

	// MinSyncedTicket is the minimum logical time taken by clients who attach the document.
	// It used to collect garbage on the replica on the client.
	MinSyncedTicket *time.Ticket

	// IsRemoved is a flag that indicates whether the document is removed.
	IsRemoved bool
}

// NewPack creates a new instance of Pack.
func NewPack(
	key key.Key,
	cp Checkpoint,
	changes []*Change,
	snapshot []byte,
) *Pack {
	return &Pack{
		DocumentKey: key,
		Checkpoint:  cp,
		Changes:     changes,
		Snapshot:    snapshot,
	}
}

// HasChanges returns the whether pack has changes or not.
func (p *Pack) HasChanges() bool {
	return len(p.Changes) > 0
}

// ChangesLen returns the size of the changes.
func (p *Pack) ChangesLen() int {
	return len(p.Changes)
}

// OperationsLen returns the size of the operations.
func (p *Pack) OperationsLen() int {
	operations := 0
	for _, c := range p.Changes {
		operations += len(c.operations)
	}
	return operations
}

// IsAttached returns whether the document is attached or not.
func (p *Pack) IsAttached() bool {
	return p.Checkpoint.ServerSeq != 0
}
