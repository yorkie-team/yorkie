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

package auth

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/operations"
	"github.com/yorkie-team/yorkie/pkg/document/presence/inner"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

func newPresenceOnlyChange() *change.Change {
	return change.New(
		change.InitialID(),
		"",
		nil,
		&inner.Change{ChangeType: inner.Put, Presence: inner.Presence{"cursor": "1"}},
	)
}

func newOperationChange() *change.Change {
	return change.New(
		change.InitialID(),
		"",
		[]operations.Operation{
			operations.NewRemove(time.InitialTicket, time.InitialTicket, time.InitialTicket),
		},
		nil,
	)
}

func newPack(changes ...*change.Change) *change.Pack {
	return change.NewPack(key.Key("doc-1"), change.InitialCheckpoint, changes, nil, nil)
}

func newRemovalPack(changes ...*change.Change) *change.Pack {
	pack := newPack(changes...)
	pack.IsRemoved = true
	return pack
}

func TestAccessAttributes(t *testing.T) {
	tests := []struct {
		name           string
		pack           *change.Pack
		verb           types.VerbType
		presenceOnly   bool
		writesDocument bool
	}{
		{"empty pack", newPack(), types.Read, false, false},
		{"presence-only pack", newPack(newPresenceOnlyChange()), types.ReadWrite, true, false},
		{"pack with operations", newPack(newOperationChange()), types.ReadWrite, false, true},
		{
			"operations behind presence",
			newPack(newPresenceOnlyChange(), newOperationChange()),
			types.ReadWrite,
			false,
			true,
		},
		{"removal without changes", newRemovalPack(), types.ReadWrite, false, true},
		{"removal with presence only", newRemovalPack(newPresenceOnlyChange()), types.ReadWrite, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := AccessAttributes(tt.pack)
			require.Len(t, attrs, 1)
			assert.Equal(t, "doc-1", attrs[0].Key)
			assert.Equal(t, tt.verb, attrs[0].Verb)
			require.NotNil(t, attrs[0].PresenceOnly)
			assert.Equal(t, tt.presenceOnly, *attrs[0].PresenceOnly)
			assert.Equal(t, tt.writesDocument, WritesDocument(attrs))
		})
	}
}

func TestAccessAttributesJSON(t *testing.T) {
	t.Run("a pack sends presenceOnly even when false", func(t *testing.T) {
		body, err := json.Marshal(AccessAttributes(newPack(newOperationChange())))
		require.NoError(t, err)
		assert.JSONEq(t, `[{"key":"doc-1","verb":"rw","presenceOnly":false}]`, string(body))
	})

	t.Run("a method without a pack omits presenceOnly", func(t *testing.T) {
		attrs := types.NewAccessAttributes([]key.Key{"doc-1"}, types.ReadWrite)
		body, err := json.Marshal(attrs)
		require.NoError(t, err)
		assert.JSONEq(t, `[{"key":"doc-1","verb":"rw"}]`, string(body))
		assert.True(t, WritesDocument(attrs))
	})
}

// TestVerifyAccessPresenceOnlyCache checks that a decision cached for a
// presence-only pack is not reused for a pack that edits the document. Both
// report verb rw, so only PresenceOnly tells them apart in the cache key; a
// webhook that allows presence but rejects edits from a read-only member would
// otherwise be bypassed by the cache for the next AuthWebhookCacheTTL.
func TestVerifyAccessPresenceOnlyCache(t *testing.T) {
	ctx := t.Context()
	be, project, stub := newWebhookTest(t)
	project.AuthWebhookMethods = []string{string(types.PushPull)}

	pushPull := func(c *change.Change) *types.AccessInfo {
		return &types.AccessInfo{Method: types.PushPull, Attributes: AccessAttributes(newPack(c))}
	}

	require.NoError(t, verifyAccess(ctx, be, project, "alice", pushPull(newPresenceOnlyChange()), false))
	assert.Equal(t, int32(1), stub.calls.Load())

	require.NoError(t, verifyAccess(ctx, be, project, "alice", pushPull(newOperationChange()), false))
	assert.Equal(t, int32(2), stub.calls.Load(), "a pack with operations reused a presence-only answer")

	require.NoError(t, verifyAccess(ctx, be, project, "alice", pushPull(newPresenceOnlyChange()), false))
	assert.Equal(t, int32(2), stub.calls.Load(), "a repeated presence-only pack missed the cache")

	// The key needle of DropCachedDecisions still finds both decisions.
	assert.Equal(t, 2, DropCachedDecisions(be, project, []string{"doc-1"}))
}
