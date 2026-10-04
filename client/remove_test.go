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
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
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
	ctx, cancel := context.WithTimeout(context.Background(), 5*gotime.Second)
	defer cancel()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(&removeTestServer{}))
	httpServer := httptest.NewServer(mux)
	defer httpServer.Close()

	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	cli.storeStatus(statusActivated)

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

// TestStopWatchPipelineOutlivesStreamReaders pins the teardown order. The pump
// is the sole consumer of Document.Events and Document.publish is an
// unconditional send on a capacity-one channel made under the document's event
// mutex, so retiring the pump while a stream reader is still reconciling
// presence wedges that reader -- and, through the event mutex, every other
// publisher -- for good. The teardown must drain the readers first.
func TestStopWatchPipelineOutlivesStreamReaders(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*gotime.Second)
	defer cancel()

	doc := document.New(key.Key("stop-waits-for-readers"))
	doc.SetStatus(document.StatusAttached)
	peerID := givePeerPresence(t, doc)

	watchCtx, closeWatch := context.WithCancel(ctx)
	defer closeWatch()
	attachment := &Attachment{
		resourceID:       types.ID("000000000000000000000000"),
		resource:         doc,
		watchCtx:         watchCtx,
		closeWatchStream: closeWatch,
	}
	startWatchPipeline(watchCtx, attachment, doc)

	// Stands in for the stream reader runWatchLoop registers: it keeps
	// publishing presence reconciliations after watchCtx is cancelled, exactly
	// as one still working through handleWatchResponse does.
	readerDone := make(chan struct{})
	started := make(chan struct{})
	attachment.watchReaders.Add(1)
	go func() {
		defer close(readerDone)
		defer attachment.watchReaders.Done()

		close(started)
		for range 64 {
			doc.AddOnlineClientAndReconcile(peerID)
			doc.RemoveOnlineClientAndReconcile(peerID)
		}
	}()
	<-started

	stopWatchPipeline(attachment)

	select {
	case <-readerDone:
	case <-ctx.Done():
		t.Fatal("stream reader wedged: the pump was retired while it was still publishing")
	}
}

// givePeerPresence hands the document a peer that has presence, so toggling
// that peer online and offline emits a watched/unwatched event every time.
func givePeerPresence(t *testing.T, d *document.Document) string {
	t.Helper()

	actor, err := time.ActorIDFromHex("000000000000000000000009")
	require.NoError(t, err)

	peer := document.New(d.Key())
	peer.SetActor(actor)
	require.NoError(t, peer.Update(func(_ *json.Object, p *presence.Presence) error {
		p.Set("name", "peer")
		return nil
	}))

	pack := peer.CreateChangePack()
	copied := *pack
	copied.VersionVector = pack.VersionVector.DeepCopy()
	copied.VersionVector.Set(d.ActorID(), d.VersionVector().VersionOf(d.ActorID()))
	require.NoError(t, d.ApplyChangePack(&copied))

	return actor.String()
}
