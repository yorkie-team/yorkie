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
		name string
		pack *change.Pack
		verb types.VerbType
	}{
		{"empty pack", newPack(), types.Read},
		// Presence is stored and broadcast, so on the methods that carry the
		// changes the client chose to send it is a write.
		{"presence-only pack", newPack(newPresenceOnlyChange()), types.ReadWrite},
		{"pack with operations", newPack(newOperationChange()), types.ReadWrite},
		{"operations behind presence", newPack(newPresenceOnlyChange(), newOperationChange()), types.ReadWrite},
		{"removal without changes", newRemovalPack(), types.ReadWrite},
		{"removal with presence only", newRemovalPack(newPresenceOnlyChange()), types.ReadWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, []types.AccessAttribute{{Key: "doc-1", Verb: tt.verb}}, AccessAttributes(tt.pack))
		})
	}
}

func TestAttachmentAccessAttributes(t *testing.T) {
	tests := []struct {
		name string
		pack *change.Pack
		verb types.VerbType
	}{
		{"empty pack", newPack(), types.Read},
		{"presence-only pack", newPack(newPresenceOnlyChange()), types.Read},
		// Only the one presence change the SDKs send on their own is a read.
		// A second presence change is one PushPull rejected and the client
		// kept pending, which the detach pack carries along.
		{
			"deferred presence behind the SDK's presence",
			newPack(newPresenceOnlyChange(), newPresenceOnlyChange()),
			types.ReadWrite,
		},
		{"pack with operations", newPack(newOperationChange()), types.ReadWrite},
		{"operations behind presence", newPack(newPresenceOnlyChange(), newOperationChange()), types.ReadWrite},
		{"removal without changes", newRemovalPack(), types.ReadWrite},
		{"removal with presence only", newRemovalPack(newPresenceOnlyChange()), types.ReadWrite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(
				t,
				[]types.AccessAttribute{{Key: "doc-1", Verb: tt.verb}},
				AttachmentAccessAttributes(tt.pack),
			)
		})
	}
}

// TestVerifyAccessVerbCache checks that a decision cached for a presence-only
// detach is not reused for a detach that carries operations. A webhook that
// allows reads but rejects writes from a read-only member would otherwise be
// bypassed by the cache for the next AuthWebhookCacheTTL.
func TestVerifyAccessVerbCache(t *testing.T) {
	ctx := t.Context()
	be, project, stub := newWebhookTest(t)
	project.AuthWebhookMethods = []string{string(types.DetachDocument)}

	detach := func(c *change.Change) *types.AccessInfo {
		return &types.AccessInfo{
			Method:     types.DetachDocument,
			Attributes: AttachmentAccessAttributes(newPack(c)),
		}
	}

	require.NoError(t, verifyAccess(ctx, be, project, "alice", detach(newPresenceOnlyChange()), false))
	assert.Equal(t, int32(1), stub.calls.Load())

	require.NoError(t, verifyAccess(ctx, be, project, "alice", detach(newOperationChange()), false))
	assert.Equal(t, int32(2), stub.calls.Load(), "a pack with operations reused a presence-only answer")

	require.NoError(t, verifyAccess(ctx, be, project, "alice", detach(newPresenceOnlyChange()), false))
	assert.Equal(t, int32(2), stub.calls.Load(), "a repeated presence-only pack missed the cache")
}

// TestPushPullPresenceIsWrite checks that a presence-only PushPull is reported
// as a write, so a webhook that rejects writes from a read-only member can stop
// it from persisting and broadcasting presence. Attach and detach are the only
// methods where presence alone is a read.
func TestPushPullPresenceIsWrite(t *testing.T) {
	pack := newPack(newPresenceOnlyChange())

	assert.Equal(t, types.ReadWrite, AccessAttributes(pack)[0].Verb)
	assert.Equal(t, types.Read, AttachmentAccessAttributes(pack)[0].Verb)
}

// TestDeferredPresenceIsWrite checks that the presence relaxed on attach and
// detach cannot be used to defer a presence write past the PushPull that
// rejected it. A client's pack carries every unacknowledged local change, so
// the change a rejected PushPull left pending is sent again with the detach's
// presence clear, and that pack is a write.
func TestDeferredPresenceIsWrite(t *testing.T) {
	clearChange := change.New(
		change.InitialID(),
		"",
		nil,
		&inner.Change{ChangeType: inner.Clear},
	)

	assert.Equal(t, types.Read, AttachmentAccessAttributes(newPack(clearChange))[0].Verb)
	assert.Equal(
		t,
		types.ReadWrite,
		AttachmentAccessAttributes(newPack(newPresenceOnlyChange(), clearChange))[0].Verb,
		"a detach carrying a rejected presence change was reported as a read",
	)
}
