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
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/attachable"
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
	// attachChanges is carried by the AttachDocument response, so a test can
	// drive attach's ApplyChangePack with real remote changes instead of the
	// empty pack the default handler answers with.
	attachChanges []*api.Change
	// attachRemoved marks that response pack removed, which is how the server
	// tells an attacher the document is already gone.
	attachRemoved bool
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
			Changes:       s.attachChanges,
			IsRemoved:     s.attachRemoved,
		},
	}), nil
}

func (s *watchInitServer) DetachDocument(
	_ context.Context,
	req *connect.Request[api.DetachDocumentRequest],
) (*connect.Response[api.DetachDocumentResponse], error) {
	pack := req.Msg.ChangePack
	return connect.NewResponse(&api.DetachDocumentResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    pack.Checkpoint,
			VersionVector: pack.VersionVector,
		},
	}), nil
}

func (s *watchInitServer) Watch(
	_ context.Context,
	_ *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	if s.firstResponse == nil {
		return connect.NewError(connect.CodePermissionDenied, errors.New("watch rejected"))
	}
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

	return dialAttachPackServer(t, &watchInitServer{firstResponse: first, release: make(chan struct{})})
}

// dialAttachPackServer starts the given server and returns an activated client
// pointed at it, so a test can hand AttachDocument a response pack of its own.
func dialAttachPackServer(t *testing.T, srv *watchInitServer) (*client.Client, chan struct{}) {
	t.Helper()

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

// TestWatchLoopInitFailureKeepsAttachment pins what an Attach whose watch
// stream cannot come up leaves behind. AttachDocument has already succeeded by
// then, so the server holds the attachment and rejects attaching it again; the
// client keeps it registered for the caller to Detach. Until then the stream
// is ended but the pump keeps draining the document, because the pipeline now
// starts before the stream's first response is read and a publisher must never
// be left without a consumer.
func TestWatchLoopInitFailureKeepsAttachment(t *testing.T) {
	for _, tc := range []struct {
		name     string
		response *api.WatchResponse
	}{
		{name: "malformed", response: &api.WatchResponse{}},
		{name: "rejected"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cli, _ := dialWatchInitServer(t, tc.response)

			doc := document.New(key.Key("watch-init-failure-" + tc.name))
			peer := newPresentPeer(t, "watch-init-failure-"+tc.name)

			err := cli.Attach(context.Background(), doc, client.WithRealtimeSync())
			assert.Error(t, err, "a watch stream that fails initialization must surface the error")
			if tc.name == "rejected" {
				// The RPC's own failure, not the generic "no first response":
				// a rejected Watch carries its code on stream.Err().
				assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err),
					"the rejected RPC's error must reach the caller")
			}

			// The attachment stays registered, as the server's does, and its
			// stream has ended.
			assert.Equal(t, attachable.StatusAttached, doc.Status())
			rch, _, watchErr := cli.WatchStream(doc)
			assert.NoError(t, watchErr, "the attachment must stay registered for Detach")
			select {
			case _, ok := <-rch:
				assert.False(t, ok, "a stream that never came up must deliver nothing")
			case <-gotime.After(5 * gotime.Second):
				t.Fatal("the stream of a failed watch initialization was not closed")
			}

			// The pump still consumes the document: more events than the
			// channel's capacity of one are published without blocking.
			peerID := peer.ActorID().String()
			published := make(chan struct{})
			go func() {
				doc.AddOnlineClientAndReconcile(peerID)
				assert.NoError(t, doc.ApplyChangePack(presencePackFor(peer, doc)))
				doc.RemoveOnlineClientAndReconcile(peerID)
				close(published)
			}()
			select {
			case <-published:
			case <-gotime.After(5 * gotime.Second):
				t.Fatal("a publisher wedged after the failed watch initialization")
			}

			// The caller recovers by detaching, which tears the pipeline down.
			assert.NoError(t, cli.Detach(context.Background(), doc))
			assert.Equal(t, attachable.StatusDetached, doc.Status())
			_, _, watchErr = cli.WatchStream(doc)
			assert.ErrorIs(t, watchErr, client.ErrNotAttached)
		})
	}
}

// watchReconnectServer fails its first initialized stream and holds its
// successor open until cancellation. The successor's handshake is held by the
// test until holdSecond is released, so the reconnect window -- the interval
// the client spends with no established stream -- is under the test's
// control. Both streams use the real Connect RPC.
type watchReconnectServer struct {
	*watchInitServer
	calls        atomic.Int32
	failFirst    chan struct{}
	secondCalled chan struct{}
	holdSecond   chan struct{}
	stopped      chan struct{}
	peerID       string
}

func (s *watchReconnectServer) Watch(
	ctx context.Context,
	_ *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	call := s.calls.Add(1)
	if call == 2 {
		close(s.secondCalled)
		select {
		case <-s.holdSecond:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := stream.Send(&api.WatchResponse{Body: &api.WatchResponse_Initialization{
		Initialization: &api.WatchInitialization{},
	}}); err != nil {
		return err
	}
	if call == 1 {
		select {
		case <-s.failFirst:
			return connect.NewError(connect.CodeUnavailable, errors.New("first stream ended"))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if call != 2 {
		return connect.NewError(connect.CodeInternal, errors.New("unexpected extra watch"))
	}
	defer close(s.stopped)
	if err := stream.Send(&api.WatchResponse{Body: &api.WatchResponse_Event{
		Event: &api.WatchEvent{Event: &api.WatchEvent_DocEvent{
			DocEvent: &api.DocWatchEvent{Event: &api.DocEvent{
				Type: api.DocEventType_DOC_EVENT_TYPE_DOCUMENT_WATCHED, Publisher: s.peerID,
			}},
		}},
	}}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}

// TestWatchLoopReconnectKeepsDrainingAndDelivering pins the contract that
// makes the delivery pipeline the attachment's rather than one watch loop's:
//
//   - a document event published while the client has no established stream
//     still finds a consumer, so a publisher holding Document.eventsMu is not
//     stalled by the reconnect handshake, and
//   - every such event is delivered once the successor comes up, on the same
//     response channel the consumer already held.
//
// The successor's handshake is held open by the server for the whole span of
// the publishes, so the reconnect window is inside the assertions rather than
// a race the test happens to win.
func TestWatchLoopReconnectKeepsDrainingAndDelivering(t *testing.T) {
	doc := document.New("watch-reconnect")
	peer := newPresentPeer(t, "watch-reconnect")
	peerID := peer.ActorID().String()
	srv := &watchReconnectServer{
		watchInitServer: &watchInitServer{},
		failFirst:       make(chan struct{}),
		secondCalled:    make(chan struct{}),
		holdSecond:      make(chan struct{}),
		stopped:         make(chan struct{}),
		peerID:          peerID,
	}
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	cli, err := client.Dial(httpServer.URL)
	if !assert.NoError(t, err) || !assert.NoError(t, cli.Activate(ctx)) {
		return
	}
	if !assert.NoError(t, cli.Attach(ctx, doc, client.WithRealtimeSync())) {
		return
	}
	rch, _, err := cli.WatchStream(doc)
	if !assert.NoError(t, err) {
		return
	}
	// Hand the observer the peer's presence while the peer is still offline,
	// so that reconciling it online later emits a document event.
	if !assert.NoError(t, doc.ApplyChangePack(presencePackFor(peer, doc))) {
		return
	}
	next := func(what string) client.WatchDocResponse {
		t.Helper()
		select {
		case response, ok := <-rch:
			if !ok {
				t.Fatalf("response channel closed while waiting for %s", what)
			}
			return response
		case <-gotime.After(5 * gotime.Second):
			t.Fatalf("no response while waiting for %s", what)
		}
		return client.WatchDocResponse{}
	}

	close(srv.failFirst)
	assert.Error(t, next("the first stream's error").Err,
		"the broken stream must report its error to the consumer")

	// The successor's handshake is in flight and held by the server: the
	// client has no established stream at all. Publishing must still complete,
	// and nothing published here may be lost.
	select {
	case <-srv.secondCalled:
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("the client did not re-establish the watch stream")
	}
	var want []client.WatchDocResponseType
	for range 3 {
		assertPublishes(t, "watched inside the reconnect window", func() {
			doc.AddOnlineClientAndReconcile(peerID)
		})
		assertPublishes(t, "unwatched inside the reconnect window", func() {
			doc.RemoveOnlineClientAndReconcile(peerID)
		})
		want = append(want, client.DocumentWatched, client.DocumentUnwatched)
	}
	close(srv.holdSecond)

	for i, w := range want {
		response := next("an event queued inside the reconnect window")
		assert.NoError(t, response.Err)
		assert.Equal(t, w, response.Type, "reconnect-window event %d lost or reordered", i)
	}
	successor, _, err := cli.WatchStream(doc)
	if !assert.NoError(t, err) {
		return
	}
	assert.Equal(t, rch, successor, "a reconnect must keep the consumer's channel")

	// The successor's own traffic continues on that same channel, and every
	// later transition reaches it exactly once.
	assert.Equal(t, client.DocumentWatched, next("the successor's watched event").Type)
	for range 10 {
		assertPublishes(t, "successor unwatched", func() { doc.RemoveOnlineClientAndReconcile(peerID) })
		assert.Equal(t, client.DocumentUnwatched, next("successor unwatched").Type)
		assertPublishes(t, "successor watched", func() { doc.AddOnlineClientAndReconcile(peerID) })
		assert.Equal(t, client.DocumentWatched, next("successor watched").Type)
	}
	cancel()
	select {
	case _, ok := <-rch:
		assert.False(t, ok, "cancellation must close the sender")
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("sender stayed open after cancellation")
	}
	select {
	case <-srv.stopped:
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("successor RPC stayed open after cancellation")
	}
	assert.Equal(t, int32(2), srv.calls.Load())
}

// newPresentPeer builds a second replica of the given document key carrying
// presence data, so that reconciling it online on the observer emits an event.
func newPresentPeer(t *testing.T, docKey string) *document.Document {
	t.Helper()

	return newPresentPeerWithActor(t, docKey, "000000000000000000000009")
}

// newPresentPeerWithActor is newPresentPeer for a caller that needs several
// distinct peers on one document, so each presence change in a pack reconciles
// to an event of its own.
func newPresentPeerWithActor(t *testing.T, docKey, actorHex string) *document.Document {
	t.Helper()

	actor, err := time.ActorIDFromHex(actorHex)
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

// wireChangesOf converts a peer's pending changes into the wire form an
// AttachDocument response carries.
func wireChangesOf(t *testing.T, peer *document.Document) []*api.Change {
	t.Helper()

	pack, err := converter.ToChangePack(peer.CreateChangePack())
	assert.NoError(t, err)
	return pack.Changes
}

// orphanChange returns a change whose operation targets an object created by a
// change the receiver never gets, so applying it fails: it is how a test makes
// attach's ApplyChangePack return an error after earlier changes in the same
// pack have already been applied.
func orphanChange(t *testing.T, docKey string) *api.Change {
	t.Helper()

	orphan := document.New(key.Key(docKey))
	actor, err := time.ActorIDFromHex("00000000000000000000000b")
	assert.NoError(t, err)
	orphan.SetActor(actor)
	assert.NoError(t, orphan.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetNewObject("nested")
		return nil
	}))
	assert.NoError(t, orphan.Update(func(r *json.Object, _ *presence.Presence) error {
		r.GetObject("nested").SetString("k", "v")
		return nil
	}))

	changes := wireChangesOf(t, orphan)
	return changes[len(changes)-1]
}

// attachPackEvents counts the watched events an attach response pack carrying
// the given peers' presence produces on a document that already has them
// online.
const attachPackEvents = 2

// newAttachPackDoc builds the observer document for an attach whose response
// pack carries the given peers' presence, with both peers already online so
// each presence change in that pack reconciles to a watched event.
func newAttachPackDoc(docKey string, peers ...*document.Document) *document.Document {
	doc := document.New(key.Key(docKey))
	for _, peer := range peers {
		doc.AddOnlineClientAndReconcile(peer.ActorID().String())
	}
	return doc
}

// TestAttachStartsPumpBeforeApplyingPack pins the order inside attachDocument:
// the delivery pipeline comes up before the attach response's ChangePack is
// applied. ApplyChangePack publishes one event per applied remote change onto
// the document's capacity-one event channel, under the document's event mutex,
// and that send has no cancellation path -- so a pack carrying two or more
// events applied with no pump draining them wedges the attaching goroutine for
// good, holding the event mutex against every other publisher.
func TestAttachStartsPumpBeforeApplyingPack(t *testing.T) {
	docKey := "attach-remote-pack"
	first := newPresentPeerWithActor(t, docKey, "000000000000000000000009")
	second := newPresentPeerWithActor(t, docKey, "00000000000000000000000a")

	cli, _ := dialAttachPackServer(t, &watchInitServer{
		firstResponse: &api.WatchResponse{
			Body: &api.WatchResponse_Initialization{
				Initialization: &api.WatchInitialization{},
			},
		},
		release:       make(chan struct{}),
		attachChanges: append(wireChangesOf(t, first), wireChangesOf(t, second)...),
	})

	doc := newAttachPackDoc(docKey, first, second)

	// Off-goroutine so a wedged Attach shows up as this timeout rather than as
	// a hung test binary.
	attached := make(chan error, 1)
	go func() { attached <- cli.Attach(context.Background(), doc, client.WithRealtimeSync()) }()
	select {
	case err := <-attached:
		assert.NoError(t, err)
	case <-gotime.After(10 * gotime.Second):
		t.Fatal("Attach wedged applying an attach pack with no event pump draining the document")
	}

	// Nothing the pack produced is lost on the way: the pump that drained them
	// is the attachment's, so the events are waiting on the consumer's channel.
	rch, _, err := cli.WatchStream(doc)
	assert.NoError(t, err)
	for i := range attachPackEvents {
		select {
		case response, ok := <-rch:
			assert.True(t, ok, "the stream closed before attach pack event %d", i)
			assert.NoError(t, response.Err)
			assert.Equal(t, client.DocumentWatched, response.Type)
		case <-gotime.After(5 * gotime.Second):
			t.Fatalf("attach pack event %d never reached the stream", i)
		}
	}
}

// TestAttachStopsPipelineOnFailedPaths pins the two ways attachDocument leaves
// without ever publishing the attachment: the response pack fails to apply, and
// the response reports the document already removed. The pipeline is up by then
// -- it has to be, so the pack has a consumer -- and nothing else holds the
// attachment, so attachDocument must retire it itself. A pipeline left running
// would keep a pump draining a document this client no longer owns, and a later
// attach of the same document would then race two pumps for its events.
func TestAttachStopsPipelineOnFailedPaths(t *testing.T) {
	for _, tc := range []struct {
		name    string
		removed bool
		broken  bool
		// online says whether the peers are online before the attach, which is
		// what makes the pack's presence changes reconcile to events.
		online bool
	}{
		{name: "apply-failure", broken: true},
		{name: "already-removed", removed: true, online: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			docKey := "attach-failed-path-" + tc.name
			first := newPresentPeerWithActor(t, docKey, "000000000000000000000009")
			second := newPresentPeerWithActor(t, docKey, "00000000000000000000000a")

			changes := append(wireChangesOf(t, first), wireChangesOf(t, second)...)
			if tc.broken {
				changes = append(changes, orphanChange(t, docKey))
			}
			cli, _ := dialAttachPackServer(t, &watchInitServer{
				firstResponse: &api.WatchResponse{
					Body: &api.WatchResponse_Initialization{
						Initialization: &api.WatchInitialization{},
					},
				},
				release:       make(chan struct{}),
				attachChanges: changes,
				attachRemoved: tc.removed,
			})

			var doc *document.Document
			if tc.online {
				doc = newAttachPackDoc(docKey, first, second)
			} else {
				doc = document.New(key.Key(docKey))
			}

			attached := make(chan error, 1)
			go func() { attached <- cli.Attach(context.Background(), doc, client.WithRealtimeSync()) }()
			select {
			case err := <-attached:
				if tc.broken {
					assert.Error(t, err, "a pack that cannot be applied must surface its error")
				} else {
					assert.NoError(t, err)
					assert.Equal(t, attachable.StatusRemoved, doc.Status())
				}
			case <-gotime.After(10 * gotime.Second):
				t.Fatal("Attach wedged on a path that never publishes the attachment")
			}

			// No attachment was published, so there is nothing to watch.
			_, _, watchErr := cli.WatchStream(doc)
			assert.ErrorIs(t, watchErr, client.ErrNotAttached)

			// And the pipeline is gone with it: the document's capacity-one
			// event channel has no consumer, so the second emission parks.
			// Both peers carry presence by now -- the pack applied theirs
			// before it failed -- so each reconcile below is one event.
			assertNoConsumer(t, "the retired pipeline", func() {
				if tc.online {
					doc.RemoveOnlineClientAndReconcile(first.ActorID().String())
					doc.RemoveOnlineClientAndReconcile(second.ActorID().String())
					return
				}
				doc.AddOnlineClientAndReconcile(first.ActorID().String())
				doc.AddOnlineClientAndReconcile(second.ActorID().String())
			})
		})
	}
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

// assertNoConsumer is the inverse of assertPublishes: it fails the test unless
// the given emission -- which must produce more events than the document's
// channel capacity of one -- parks, which is how a retired pipeline looks from
// the publisher's side. The emitting goroutine stays parked for the rest of the
// binary, which is harmless because the document it holds is test-local and no
// other goroutine publishes on it.
func assertNoConsumer(t *testing.T, name string, emit func()) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		emit()
		close(done)
	}()
	select {
	case <-done:
		t.Fatalf("%s still drains the document event channel", name)
	case <-gotime.After(500 * gotime.Millisecond):
	}
}
