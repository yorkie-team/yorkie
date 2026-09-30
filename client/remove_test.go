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
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

type removeTestServer struct {
	v1connect.UnimplementedYorkieServiceHandler
}

func (s *removeTestServer) RemoveDocument(
	_ context.Context,
	req *connect.Request[api.RemoveDocumentRequest],
) (*connect.Response[api.RemoveDocumentResponse], error) {
	pack := req.Msg.ChangePack
	return connect.NewResponse(&api.RemoveDocumentResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    pack.Checkpoint,
			VersionVector: pack.VersionVector,
			IsRemoved:     true,
		},
	}), nil
}

// TestRemoveStopsWatchPipeline ensures Remove tears the attachment's delivery
// pipeline down. The pump and the sender are owned by the attachment, so
// dropping it from c.attachments without cancelling watchCtx would leak both
// goroutines for the lifetime of the client.
func TestRemoveStopsWatchPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(&removeTestServer{}))
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	cli.status = statusActivated

	doc := document.New(key.Key("remove-watch-pipeline"))
	doc.SetStatus(document.StatusAttached)

	watchCtx, closeWatch := context.WithCancel(ctx)
	defer closeWatch()
	attachment := &Attachment{
		resourceID:       types.ID("000000000000000000000000"),
		resource:         doc,
		watchCtx:         watchCtx,
		closeWatchStream: closeWatch,
	}
	startWatchPipeline(watchCtx, attachment, doc)
	cli.attachments.Set(doc.Key(), attachment)

	require.NoError(t, cli.Remove(ctx, doc))
	require.Equal(t, document.StatusRemoved, doc.Status())

	_, ok := cli.attachments.Get(doc.Key())
	require.False(t, ok)

	select {
	case <-attachment.watchPumpDone:
	case <-ctx.Done():
		t.Fatal("watch event pump kept running after the document was removed")
	}
	select {
	case _, ok := <-attachment.watchStream:
		require.False(t, ok, "watch stream should be closed after removal")
	case <-ctx.Done():
		t.Fatal("watch stream was not closed after the document was removed")
	}
}
