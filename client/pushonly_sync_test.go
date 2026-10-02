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

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// pushPullEchoServer answers PushPullChanges with the request's checkpoint and
// version vector, as a reply that pulled nothing.
type pushPullEchoServer struct {
	v1connect.UnimplementedYorkieServiceHandler
}

func (s *pushPullEchoServer) PushPullChanges(
	_ context.Context,
	req *connect.Request[api.PushPullChangesRequest],
) (*connect.Response[api.PushPullChangesResponse], error) {
	pack := req.Msg.ChangePack
	return connect.NewResponse(&api.PushPullChangesResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    pack.Checkpoint,
			VersionVector: pack.VersionVector,
		},
	}), nil
}

// TestPushOnlySyncKeepsPullSignal pins that a push-only sync, which pulls
// nothing, leaves a pending remote-change signal for the next pull.
func TestPushOnlySyncKeepsPullSignal(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(&pushPullEchoServer{}))
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	cli.status = statusActivated

	doc := document.New(key.Key("pushonly-pull-signal"))
	doc.SetStatus(document.StatusAttached)
	attachment := &Attachment{
		resourceID:          types.ID("000000000000000000000000"),
		resource:            doc,
		syncMode:            SyncModeRealtime,
		changeEventReceived: true,
	}
	cli.attachments.Set(doc.Key(), attachment)

	require.NoError(t, cli.Sync(ctx, WithKey(doc.Key()).WithPushOnly()))
	assert.True(t, attachment.changeEventReceived)
	assert.True(t, attachment.needSync(0))

	require.NoError(t, cli.Sync(ctx, WithKey(doc.Key())))
	assert.False(t, attachment.changeEventReceived)
	assert.False(t, attachment.needSync(0))
}
