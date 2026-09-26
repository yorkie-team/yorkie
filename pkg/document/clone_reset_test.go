/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package document_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
)

// TestCloneResetOnFailedApply covers a remote change that fails partway.
// Change.Execute does not roll back, so the clone keeps the operations that
// ran before the failure while the root, executed second, never sees them.
// The clone must be dropped so it is rebuilt from the root.
func TestCloneResetOnFailedApply(t *testing.T) {
	actor, err := time.ActorIDFromHex("000000000000000000000002")
	require.NoError(t, err)

	source := document.New("d")
	source.SetActor(actor)
	require.NoError(t, source.Update(func(root *json.Object, p *presence.Presence) error {
		root.SetString("k", "v")
		return nil
	}))
	c := source.CreateChangePack().Changes[0]

	// Append an operation whose parent does not exist, so the change fails
	// after its first operation already ran.
	missing := time.NewTicket(100, 0, actor)
	value, err := crdt.NewPrimitive("x", time.NewTicket(101, 0, actor))
	require.NoError(t, err)
	ops := append(c.Operations(), operations.NewSet(missing, "x", value, value.CreatedAt()))
	broken := change.New(c.ID(), "", ops, nil)

	target := document.New("d")
	pack := change.NewPack("d", change.InitialCheckpoint, []*change.Change{broken}, nil, nil)
	assert.Error(t, target.ApplyChangePack(pack))

	// Root() reads the clone and Marshal() the root; they must agree.
	assert.Equal(t, target.Marshal(), target.Root().Marshal())
}
