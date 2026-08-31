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

type detachTestServer struct {
	v1connect.UnimplementedYorkieServiceHandler

	detachStarted chan struct{}
	allowDetach   chan struct{}
}

func (s *detachTestServer) DetachDocument(
	ctx context.Context,
	req *connect.Request[api.DetachDocumentRequest],
) (*connect.Response[api.DetachDocumentResponse], error) {
	close(s.detachStarted)
	select {
	case <-s.allowDetach:
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	pack := req.Msg.ChangePack
	return connect.NewResponse(&api.DetachDocumentResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    pack.Checkpoint,
			VersionVector: pack.VersionVector,
		},
	}), nil
}

func TestDetachClosesWatchAfterApplyingChangePack(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	server := &detachTestServer{
		detachStarted: make(chan struct{}),
		allowDetach:   make(chan struct{}),
	}
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(server))
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	cli.status = statusActivated

	doc := document.New(key.Key("detach-watch-order"))
	doc.SetStatus(document.StatusAttached)

	watchClosed := make(chan struct{})
	cli.attachments.Set(doc.Key(), &Attachment{
		resourceID: types.ID("000000000000000000000000"),
		resource:   doc,
		closeWatchStream: func() {
			close(watchClosed)
		},
	})

	detachDone := make(chan error, 1)
	go func() {
		detachDone <- cli.Detach(ctx, doc)
	}()

	select {
	case <-server.detachStarted:
	case <-ctx.Done():
		t.Fatal("detach request did not reach the server")
	}
	select {
	case <-watchClosed:
		t.Fatal("watch stream closed before the final ChangePack was received")
	default:
	}

	close(server.allowDetach)
	select {
	case err := <-detachDone:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("detach did not finish")
	}

	select {
	case <-watchClosed:
	default:
		t.Fatal("watch stream remained open after detaching the document")
	}
}
