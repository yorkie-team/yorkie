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

package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// attachedClient returns an activated Client holding one attachment under
// testKey, plus a second resource carrying that same key and no attachment of
// its own -- what a resource rejected by the attach guard, or a stale one from
// before a deactivation, looks like.
//
// The server it dials implements nothing, so any call that reaches the network
// fails loudly rather than hanging: an identity check that stopped rejecting
// would surface as an unimplemented RPC error, never as a pass.
func attachedClient(t *testing.T) (*Client, *document.Document) {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(v1connect.UnimplementedYorkieServiceHandler{}))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	cli.storeStatus(statusActivated)

	attached := document.New(key.Key(t.Name()))
	attached.SetStatus(document.StatusAttached)
	cli.attachments.Set(attached.Key(), &Attachment{
		resourceID: types.ID("000000000000000000000000"),
		resource:   attached,
	})

	return cli, attached
}

// TestRemoveRejectsSameKeyResource covers Remove's identity check. The
// attachment is looked up by key alone, so without it the rejected handle would
// send the attached document's ID and remove that document on the server.
func TestRemoveRejectsSameKeyResource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, attached := attachedClient(t)
	rejected := document.New(attached.Key())

	assert.ErrorIs(t, cli.Remove(ctx, rejected), ErrNotAttached)

	// The attachment the rejected handle shares a key with is untouched.
	attachment, ok := cli.attachments.Get(attached.Key())
	require.True(t, ok)
	assert.Same(t, attached, attachment.resource)
	assert.Equal(t, document.StatusAttached, attached.Status())
}

// TestDetachRejectsSameKeyResource covers the same check on Detach, which
// would likewise detach the attached document in the rejected handle's name.
func TestDetachRejectsSameKeyResource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, attached := attachedClient(t)
	rejected := document.New(attached.Key())

	assert.ErrorIs(t, cli.Detach(ctx, rejected), ErrNotAttached)

	attachment, ok := cli.attachments.Get(attached.Key())
	require.True(t, ok)
	assert.Same(t, attached, attachment.resource)
}

// TestWatchStreamRejectsSameKeyResource covers WatchStream's identity check:
// without it the rejected handle observes another resource's event stream.
func TestWatchStreamRejectsSameKeyResource(t *testing.T) {
	cli, attached := attachedClient(t)
	rejected := document.New(attached.Key())

	stream, closeStream, err := cli.WatchStream(rejected)
	assert.ErrorIs(t, err, ErrNotAttached)
	assert.Nil(t, stream)
	assert.Nil(t, closeStream)
}

// TestBroadcastRejectsSameKeyChannel covers broadcast's identity check. A
// Channel shares the key namespace with Documents, so a channel rejected by the
// attach guard carries a key another resource's attachment holds; without the
// check it would publish through that attachment.
func TestBroadcastRejectsSameKeyChannel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cli, attached := attachedClient(t)
	rejected, err := channel.New(attached.Key())
	require.NoError(t, err)

	assert.ErrorIs(t, cli.broadcast(ctx, rejected, "topic", []byte("payload")), ErrNotAttached)
}
