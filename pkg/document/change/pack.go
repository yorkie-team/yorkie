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
	"github.com/yorkie-team/yorkie/pkg/document/checkpoint"
	"github.com/yorkie-team/yorkie/pkg/document/key"
)

// Pack is a unit for delivering changes in a document to the remote.
type Pack struct {
	DocumentKey *key.Key
	Checkpoint  *checkpoint.Checkpoint
	Changes     []*Change
}

// NewPack creates a new instance of Pack.
func NewPack(
	key *key.Key,
	cp *checkpoint.Checkpoint,
	changes []*Change,
) *Pack {
	return &Pack{
		DocumentKey: key,
		Checkpoint:  cp,
		Changes:     changes,
	}
}

// HasChanges returns the whether pack has changes or not.
func (p *Pack) HasChanges() bool {
	return len(p.Changes) > 0
}
