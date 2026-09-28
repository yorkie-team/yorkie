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

package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/document/time"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// watchInitServer answers just enough of the Yorkie service to drive a client
// through Attach into runWatchLoop, with the watch stream's first response
// under the test's control.
type watchInitServer struct {
	v1connect.UnimplementedYorkieServiceHandler

	// firstResponse is the stream's first message. A nil Body is one the
	// client cannot classify, which fails handleWatchResponse.
	firstResponse *api.WatchResponse
	// release holds the handler open after that message, keeping the stream
	// established but silent.
	release chan struct{}
}

func (s *watchInitServer) ActivateClient(
	_ context.Context,
	_ *connect.Request[api.ActivateClientRequest],
) (*connect.Response[api.ActivateClientResponse], error) {
	return connect.NewResponse(&api.ActivateClientResponse{
		ClientId: "000000000000000000000001",
	}), nil
}

func (s *watchInitServer) AttachDocument(
	_ context.Context,
	req *connect.Request[api.AttachDocumentRequest],
) (*connect.Response[api.AttachDocumentResponse], error) {
	return connect.NewResponse(&api.AttachDocumentResponse{
		DocumentId: "000000000000000000000002",
		ChangePack: &api.ChangePack{
			DocumentKey:   req.Msg.ChangePack.DocumentKey,
			Checkpoint:    &api.Checkpoint{ServerSeq: 1, ClientSeq: 1},
			VersionVector: req.Msg.ChangePack.VersionVector,
		},
	}), nil
}

func (s *watchInitServer) Watch(
	_ context.Context,
	_ *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	if err := stream.Send(s.firstResponse); err != nil {
		return err
	}

	<-s.release
	return nil
}

// dialWatchInitServer starts a server whose watch stream answers with the
// given first response and then goes silent, and returns an activated client
// pointed at it.
func dialWatchInitServer(t *testing.T, first *api.WatchResponse) (*client.Client, chan struct{}) {
	t.Helper()

	srv := &watchInitServer{firstResponse: first, release: make(chan struct{})}
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)

	cli, err := client.Dial(httpServer.URL)
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(context.Background()))

	t.Cleanup(func() {
		select {
		case <-srv.release:
		default:
			close(srv.release)
		}
		httpServer.Close()
	})

	return cli, srv.release
}

// TestWatchLoopPumpDrainsWhileStreamIsIdle pins that runWatchLoop's event pump
// is a consumer of the document event channel in its own right, not something
// driven by stream traffic. The channel has capacity one and a publisher that
// blocks on it holds Document.eventsMu, stalling every other publisher -- the
// sync goroutine's ApplyChangePack included -- so a document that emits more
// than one event while the server is silent must not wedge.
func TestWatchLoopPumpDrainsWhileStreamIsIdle(t *testing.T) {
	cli, _ := dialWatchInitServer(t, &api.WatchResponse{
		Body: &api.WatchResponse_Initialization{
			Initialization: &api.WatchInitialization{},
		},
	})

	doc := document.New("watch-idle")
	peer := newPresentPeer(t, "watch-idle")
	assert.NoError(t, cli.Attach(context.Background(), doc, client.WithRealtimeSync()))

	peerID := peer.ActorID().String()
	doc.AddOnlineClientAndReconcile(peerID)
	assertPublishes(t, "presence apply", func() {
		assert.NoError(t, doc.ApplyChangePack(presencePackFor(peer, doc)))
	})
	assertPublishes(t, "unwatched", func() {
		doc.RemoveOnlineClientAndReconcile(peerID)
	})
	assertPublishes(t, "watched", func() {
		doc.AddOnlineClientAndReconcile(peerID)
	})
}

// TestWatchLoopInitFailureStopsPump pins the other half of the same ordering:
// the pump now starts before the stream's first response is read, so every
// path that abandons the loop during initialization has to stop it again.
// A pump left running past a failed initialization is a goroutine consuming
// the document's events for a stream nobody reads.
func TestWatchLoopInitFailureStopsPump(t *testing.T) {
	// A response with no body: the client cannot classify it, so
	// handleWatchResponse fails and runWatchLoop takes an init-failure path.
	cli, _ := dialWatchInitServer(t, &api.WatchResponse{})

	doc := document.New("watch-init-failure")
	peer := newPresentPeer(t, "watch-init-failure")

	err := cli.Attach(context.Background(), doc, client.WithRealtimeSync())
	assert.Error(t, err, "a watch stream that fails initialization must surface the error")

	// The pump is gone with the failed loop: the channel takes the single
	// event its capacity holds and the next publish has no consumer.
	peerID := peer.ActorID().String()
	doc.AddOnlineClientAndReconcile(peerID)
	assert.NoError(t, doc.ApplyChangePack(presencePackFor(peer, doc)))

	blocked := make(chan struct{})
	go func() {
		doc.RemoveOnlineClientAndReconcile(peerID)
		close(blocked)
	}()
	select {
	case <-blocked:
		t.Fatal("the event pump outlived the failed watch initialization")
	case <-gotime.After(300 * gotime.Millisecond):
	}

	// Drain so the blocked publisher finishes and releases Document.eventsMu.
	<-doc.Events()
	<-doc.Events()
	select {
	case <-blocked:
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("publisher stayed blocked after the channel was drained")
	}
}

// newPresentPeer builds a second replica of the given document key carrying
// presence data, so that reconciling it online on the observer emits an event.
func newPresentPeer(t *testing.T, docKey string) *document.Document {
	t.Helper()

	actor, err := time.ActorIDFromHex("000000000000000000000009")
	assert.NoError(t, err)

	peer := document.New(key.Key(docKey))
	peer.SetActor(actor)
	assert.NoError(t, peer.Update(func(_ *json.Object, p *presence.Presence) error {
		p.Set("name", "peer")
		return nil
	}))
	return peer
}

// presencePackFor builds the change pack carrying the peer's presence, with
// the version vector adjusted for the receiver as in real delivery.
func presencePackFor(from, to *document.Document) *change.Pack {
	pack := from.CreateChangePack()
	copied := *pack
	copied.VersionVector = pack.VersionVector.DeepCopy()
	copied.VersionVector.Set(to.ActorID(), to.VersionVector().VersionOf(to.ActorID()))
	return &copied
}

// assertPublishes fails the test if the given emission does not complete
// promptly, which is what a document event channel with no consumer looks
// like from the publisher's side.
func assertPublishes(t *testing.T, name string, emit func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		emit()
		close(done)
	}()
	select {
	case <-done:
	case <-gotime.After(5 * gotime.Second):
		t.Fatalf("%s blocked: no consumer on the document event channel", name)
	}
}
