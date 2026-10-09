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
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/pkg/attachable"
	"github.com/yorkie-team/yorkie/pkg/channel"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/key"
)

// gate parks a handler until the test releases it, and tells the test when the
// handler has arrived. A nil gate lets the handler through.
type gate struct {
	entered chan struct{}
	release chan struct{}
}

func newGate() *gate {
	return &gate{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

func (g *gate) pass(ctx context.Context) error {
	if g == nil {
		return nil
	}
	select {
	case g.entered <- struct{}{}:
	default:
	}
	select {
	case <-g.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// lifecycleServer answers every RPC the client lifecycle touches and counts
// them, so a test can tell an operation that backed out before its RPC from
// one that reached the server.
type lifecycleServer struct {
	v1connect.UnimplementedYorkieServiceHandler

	activateGate *gate
	attachGate   *gate

	// failStream ends the first watch stream with an error when closed;
	// rejectReconnect makes every later Watch fail its handshake.
	failStream      chan struct{}
	rejectReconnect bool
	// removedOnPull marks every PushPullChanges reply removed.
	removedOnPull bool

	mu    sync.Mutex
	calls map[string]int
}

func newLifecycleServer() *lifecycleServer {
	return &lifecycleServer{failStream: make(chan struct{}), calls: make(map[string]int)}
}

func (s *lifecycleServer) count(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[method]
}

func (s *lifecycleServer) record(method string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[method]++
	return s.calls[method]
}

func echoPack(pack *api.ChangePack, removed bool) *api.ChangePack {
	return &api.ChangePack{
		DocumentKey:   pack.DocumentKey,
		Checkpoint:    pack.Checkpoint,
		VersionVector: pack.VersionVector,
		IsRemoved:     removed,
	}
}

func (s *lifecycleServer) ActivateClient(
	ctx context.Context,
	_ *connect.Request[api.ActivateClientRequest],
) (*connect.Response[api.ActivateClientResponse], error) {
	s.record("ActivateClient")
	if err := s.activateGate.pass(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&api.ActivateClientResponse{ClientId: "000000000000000000000001"}), nil
}

func (s *lifecycleServer) DeactivateClient(
	_ context.Context,
	_ *connect.Request[api.DeactivateClientRequest],
) (*connect.Response[api.DeactivateClientResponse], error) {
	s.record("DeactivateClient")
	return connect.NewResponse(&api.DeactivateClientResponse{}), nil
}

func (s *lifecycleServer) AttachDocument(
	ctx context.Context,
	req *connect.Request[api.AttachDocumentRequest],
) (*connect.Response[api.AttachDocumentResponse], error) {
	s.record("AttachDocument")
	if err := s.attachGate.pass(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&api.AttachDocumentResponse{
		DocumentId: "000000000000000000000002",
		ChangePack: &api.ChangePack{
			DocumentKey:   req.Msg.ChangePack.DocumentKey,
			Checkpoint:    &api.Checkpoint{ServerSeq: 1, ClientSeq: 1},
			VersionVector: req.Msg.ChangePack.VersionVector,
		},
	}), nil
}

func (s *lifecycleServer) DetachDocument(
	_ context.Context,
	req *connect.Request[api.DetachDocumentRequest],
) (*connect.Response[api.DetachDocumentResponse], error) {
	s.record("DetachDocument")
	return connect.NewResponse(&api.DetachDocumentResponse{ChangePack: echoPack(req.Msg.ChangePack, false)}), nil
}

func (s *lifecycleServer) RemoveDocument(
	_ context.Context,
	req *connect.Request[api.RemoveDocumentRequest],
) (*connect.Response[api.RemoveDocumentResponse], error) {
	s.record("RemoveDocument")
	return connect.NewResponse(&api.RemoveDocumentResponse{ChangePack: echoPack(req.Msg.ChangePack, true)}), nil
}

func (s *lifecycleServer) PushPullChanges(
	_ context.Context,
	req *connect.Request[api.PushPullChangesRequest],
) (*connect.Response[api.PushPullChangesResponse], error) {
	s.record("PushPullChanges")
	return connect.NewResponse(&api.PushPullChangesResponse{
		ChangePack: echoPack(req.Msg.ChangePack, s.removedOnPull),
	}), nil
}

func (s *lifecycleServer) AttachChannel(
	ctx context.Context,
	_ *connect.Request[api.AttachChannelRequest],
) (*connect.Response[api.AttachChannelResponse], error) {
	s.record("AttachChannel")
	if err := s.attachGate.pass(ctx); err != nil {
		return nil, err
	}
	return connect.NewResponse(&api.AttachChannelResponse{SessionId: "000000000000000000000003", SessionCount: 1}), nil
}

func (s *lifecycleServer) RefreshChannel(
	_ context.Context,
	_ *connect.Request[api.RefreshChannelRequest],
) (*connect.Response[api.RefreshChannelResponse], error) {
	s.record("RefreshChannel")
	return connect.NewResponse(&api.RefreshChannelResponse{SessionCount: 1}), nil
}

func (s *lifecycleServer) DetachChannel(
	_ context.Context,
	_ *connect.Request[api.DetachChannelRequest],
) (*connect.Response[api.DetachChannelResponse], error) {
	s.record("DetachChannel")
	return connect.NewResponse(&api.DetachChannelResponse{}), nil
}

func (s *lifecycleServer) Watch(
	ctx context.Context,
	_ *connect.Request[api.WatchRequest],
	stream *connect.ServerStream[api.WatchResponse],
) error {
	call := s.record("Watch")
	if call > 1 && s.rejectReconnect {
		return connect.NewError(connect.CodePermissionDenied, errors.New("reconnect rejected"))
	}
	if err := stream.Send(&api.WatchResponse{Body: &api.WatchResponse_Initialization{
		Initialization: &api.WatchInitialization{},
	}}); err != nil {
		return err
	}
	if call == 1 {
		select {
		case <-s.failStream:
			return connect.NewError(connect.CodeUnavailable, errors.New("stream lost"))
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	<-ctx.Done()
	return ctx.Err()
}

// dialLifecycle starts the given server and returns an activated client on
// it whose sync loop never fires on its own during the test.
func dialLifecycle(t *testing.T, srv *lifecycleServer, opts ...Option) *Client {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	cli, err := Dial(httpServer.URL, append([]Option{WithSyncLoopDuration(gotime.Hour)}, opts...)...)
	require.NoError(t, err)
	require.NoError(t, cli.Activate(context.Background()))
	// Registered after the server's, so it runs first: an open watch stream
	// would otherwise hold the server's Close.
	// A failing test can leave an attachment Close does not reach -- that is
	// what some of these tests look for -- so retire whatever is left too.
	t.Cleanup(func() {
		_ = cli.Close()
		for _, attachment := range cli.attachments.Values() {
			stopWatchPipeline(attachment)
		}
	})
	return cli
}

// waitFor polls cond until it holds, failing the test after a few seconds.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := gotime.Now().Add(5 * gotime.Second)
	for !cond() {
		if gotime.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		gotime.Sleep(5 * gotime.Millisecond)
	}
}

// receive waits for one result, failing the test if it never comes.
func receive[T any](t *testing.T, what string, ch <-chan T) T {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-gotime.After(10 * gotime.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// drainUntilClosed reads the response channel until it closes and returns the
// errors it carried, failing the test if it stays open.
func drainUntilClosed(t *testing.T, what string, rch <-chan WatchDocResponse) []error {
	t.Helper()

	var errs []error
	timeout := gotime.After(5 * gotime.Second)
	for {
		select {
		case resp, ok := <-rch:
			if !ok {
				return errs
			}
			if resp.Err != nil {
				errs = append(errs, resp.Err)
			}
		case <-timeout:
			t.Fatalf("%s: the response channel stayed open", what)
			return errs
		}
	}
}

// assertDrained fails unless the document's event channel has a consumer:
// with none, the second of two events parks on the capacity-one channel.
func assertDrained(t *testing.T, name string, doc *document.Document, peerID string) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		doc.AddOnlineClientAndReconcile(peerID)
		doc.RemoveOnlineClientAndReconcile(peerID)
		close(done)
	}()
	select {
	case <-done:
	case <-gotime.After(5 * gotime.Second):
		t.Fatalf("%s: no consumer on the document event channel", name)
	}
}

// assertUndrained is the inverse of assertDrained. The emitting goroutine
// stays parked for the rest of the binary, which is harmless because the
// document is test-local.
func assertUndrained(t *testing.T, name string, doc *document.Document, peerID string) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		doc.AddOnlineClientAndReconcile(peerID)
		doc.RemoveOnlineClientAndReconcile(peerID)
		close(done)
	}()
	select {
	case <-done:
		t.Fatalf("%s: something still drains the document event channel", name)
	case <-gotime.After(500 * gotime.Millisecond):
	}
}

// TestPackPathsRecheckStatusUnderSyncMu pins the invariant every path that
// applies a change pack or tears an attachment down relies on (see
// lockLiveAttachment): the client status is re-read after syncMu is taken.
// Each operation is parked on syncMu -- past its unlocked fast-path checks --
// while Deactivate leaves statusActivated and queues up to retire the
// pipeline. Whichever of the two takes syncMu first, the operation must back
// out without reaching the server: otherwise it would apply its pack after,
// or concurrently with, the retirement of the pump that drains the document's
// capacity-one event channel, and the first two events would wedge it with the
// document's event mutex held.
func TestPackPathsRecheckStatusUnderSyncMu(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rpc     string
		channel bool
		op      func(cli *Client, r attachable.Attachable) error
	}{
		{name: "detach-document", rpc: "DetachDocument", op: func(cli *Client, r attachable.Attachable) error {
			return cli.Detach(context.Background(), r)
		}},
		{name: "remove-document", rpc: "RemoveDocument", op: func(cli *Client, r attachable.Attachable) error {
			return cli.Remove(context.Background(), r.(*document.Document))
		}},
		{name: "sync-document", rpc: "PushPullChanges", op: func(cli *Client, r attachable.Attachable) error {
			return cli.Sync(context.Background(), WithKey(r.Key()))
		}},
		{name: "detach-channel", rpc: "DetachChannel", channel: true, op: func(cli *Client, r attachable.Attachable) error {
			return cli.Detach(context.Background(), r)
		}},
		{name: "sync-channel", rpc: "RefreshChannel", channel: true, op: func(cli *Client, r attachable.Attachable) error {
			return cli.Sync(context.Background(), WithKey(r.Key()))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLifecycleServer()
			cli := dialLifecycle(t, srv)

			var r attachable.Attachable
			if tc.channel {
				ch, err := channel.New(key.Key("recheck-" + tc.name))
				require.NoError(t, err)
				require.NoError(t, cli.Attach(context.Background(), ch))
				r = ch
			} else {
				doc := document.New(key.Key("recheck-" + tc.name))
				require.NoError(t, cli.Attach(context.Background(), doc, WithRealtimeSync()))
				r = doc
			}
			attachment, ok := cli.attachments.Get(r.Key())
			require.True(t, ok)

			attachment.syncMu.Lock()
			opDone := make(chan error, 1)
			go func() { opDone <- tc.op(cli, r) }()
			// Long enough for the operation to pass its unlocked checks, which
			// still see statusActivated, and park on syncMu.
			gotime.Sleep(100 * gotime.Millisecond)

			deactivated := make(chan error, 1)
			go func() { deactivated <- cli.Deactivate(context.Background()) }()
			waitFor(t, "Deactivate to leave statusActivated", func() bool {
				return cli.loadStatus() == statusDeactivating
			})

			select {
			case err := <-opDone:
				attachment.syncMu.Unlock()
				t.Fatalf("operation returned before taking syncMu: %v", err)
			default:
			}
			attachment.syncMu.Unlock()

			assert.ErrorIs(t, receive(t, "the parked operation", opDone), ErrNotActivated)
			assert.NoError(t, receive(t, "Deactivate", deactivated))
			assert.Zero(t, srv.count(tc.rpc), "the operation reached the server after its pipeline was retired")
		})
	}
}

// TestSyncRejectsAStaleAttachment pins the other half of lockLiveAttachment's
// check. The sync loop iterates a snapshot of c.attachments, so it can hold an
// attachment that has since been detached and replaced under the same key.
// Syncing it must not fall through to the replacement: the syncMu held is the
// stale attachment's, which does not keep a concurrent Detach of the
// replacement from retiring the replacement's pump mid-apply.
func TestSyncRejectsAStaleAttachment(t *testing.T) {
	srv := newLifecycleServer()
	cli := dialLifecycle(t, srv)

	first := document.New(key.Key("stale-attachment"))
	require.NoError(t, cli.Attach(context.Background(), first, WithRealtimeSync()))
	stale, ok := cli.attachments.Get(first.Key())
	require.True(t, ok)
	require.NoError(t, cli.Detach(context.Background(), first))

	second := document.New(key.Key("stale-attachment"))
	require.NoError(t, cli.Attach(context.Background(), second, WithRealtimeSync()))

	assert.ErrorIs(t, cli.syncInternal(context.Background(), stale, nil), ErrNotAttached)
	assert.Zero(t, srv.count("PushPullChanges"))
}

// TestAttachRacingDeactivateIsRejected pins that an Attach whose round trip
// spans a Deactivate does not register its attachment afterwards. Deactivate
// retires the pipelines it finds in c.attachments; one registered after that
// walk would keep a pump running for a session that no longer exists, with
// nothing left to retire it. The same holds when a new Activate follows the
// Deactivate before the Attach resolves: the attachment belongs to the ended
// session, not to the new one.
func TestAttachRacingDeactivateIsRejected(t *testing.T) {
	for _, tc := range []struct {
		name       string
		reactivate bool
		channel    bool
	}{
		{name: "document"},
		{name: "document-reactivated", reactivate: true},
		{name: "channel", channel: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLifecycleServer()
			srv.attachGate = newGate()
			cli := dialLifecycle(t, srv)

			doc := document.New(key.Key("attach-race-" + tc.name))
			peerID := givePeerPresence(t, doc)
			var r attachable.Attachable = doc
			opts := []any{WithRealtimeSync()}
			if tc.channel {
				ch, err := channel.New(key.Key("attach-race-" + tc.name))
				require.NoError(t, err)
				r, opts = ch, nil
			}

			attached := make(chan error, 1)
			go func() { attached <- cli.Attach(context.Background(), r, opts...) }()
			receive(t, "the attach round trip", srv.attachGate.entered)

			require.NoError(t, cli.Deactivate(context.Background()))
			if tc.reactivate {
				require.NoError(t, cli.Activate(context.Background()))
			}
			close(srv.attachGate.release)

			assert.ErrorIs(t, receive(t, "Attach", attached), ErrNotActivated)
			assert.Equal(t, attachable.StatusDetached, r.Status())
			assert.Zero(t, cli.attachments.Len())
			if !tc.channel {
				assertUndrained(t, "the rejected attachment's pipeline", doc, peerID)
			}
		})
	}
}

// TestActivateIsSerializedWithDeactivate pins that a Deactivate issued while
// ActivateClient is in flight waits for it and then ends the new session. A
// Deactivate that read statusDeactivated and returned as a no-op would leave
// the client activated, with a live server-side session and sync loop, right
// after the caller was told it had been deactivated.
func TestActivateIsSerializedWithDeactivate(t *testing.T) {
	srv := newLifecycleServer()
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)

	srv.activateGate = newGate()
	cli, err := Dial(httpServer.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cli.Close() })
	// Registered last, so it runs first: a failure below must not leave the
	// parked handler holding Close and the server's shutdown.
	release := sync.OnceFunc(func() { close(srv.activateGate.release) })
	t.Cleanup(release)

	activated := make(chan error, 1)
	go func() { activated <- cli.Activate(context.Background()) }()
	receive(t, "the activate round trip", srv.activateGate.entered)

	deactivated := make(chan error, 1)
	go func() { deactivated <- cli.Deactivate(context.Background()) }()
	select {
	case err := <-deactivated:
		t.Fatalf("Deactivate returned while Activate was in flight: %v", err)
	case <-gotime.After(200 * gotime.Millisecond):
	}

	release()
	assert.NoError(t, receive(t, "Activate", activated))
	assert.NoError(t, receive(t, "Deactivate", deactivated))
	assert.False(t, cli.IsActive())
	assert.Equal(t, 1, srv.count("DeactivateClient"))
}

// TestSyncObservingRemovalRetiresPipeline pins the sync-removed path of
// pushPullChanges: a pull that reports the document removed drops the
// attachment and retires its pipeline, after the final pack is applied, so
// neither the pump nor the sender outlives the removed document.
func TestSyncObservingRemovalRetiresPipeline(t *testing.T) {
	srv := newLifecycleServer()
	srv.removedOnPull = true
	cli := dialLifecycle(t, srv)

	doc := document.New(key.Key("sync-removed"))
	peerID := givePeerPresence(t, doc)
	require.NoError(t, cli.Attach(context.Background(), doc, WithRealtimeSync()))
	rch, _, err := cli.WatchStream(doc)
	require.NoError(t, err)
	assertDrained(t, "before the removal", doc, peerID)

	require.NoError(t, cli.Sync(context.Background()))
	assert.Equal(t, document.StatusRemoved, doc.Status())
	assert.Zero(t, cli.attachments.Len())

	drainUntilClosed(t, "after the removal", rch)
	assertUndrained(t, "after the removal", doc, peerID)
}

// TestWatchReconnectFailureIsRetried pins the failed-reconnect branch of the
// stream reader: the lost stream's error reaches the consumer, and a
// handshake that fails is retried with a backoff rather than retiring the
// pipeline. The handshake after a lost stream is the one most likely to fail
// -- whatever took the stream down is usually still true a moment later --
// so giving up on it would cost the document realtime for good over an
// outage that lasted seconds. The response channel therefore stays open and
// the pump keeps draining the document until Detach retires the pipeline.
func TestWatchReconnectFailureIsRetried(t *testing.T) {
	srv := newLifecycleServer()
	srv.rejectReconnect = true
	core, logs := observer.New(zap.DebugLevel)
	cli := dialLifecycle(t, srv, WithLogger(zap.New(core)))

	doc := document.New(key.Key("reconnect-failure"))
	peerID := givePeerPresence(t, doc)
	require.NoError(t, cli.Attach(context.Background(), doc, WithRealtimeSync()))
	rch, _, err := cli.WatchStream(doc)
	require.NoError(t, err)

	close(srv.failStream)
	select {
	case resp, ok := <-rch:
		assert.True(t, ok, "a failed reconnect must not close the response channel")
		assert.Error(t, resp.Err, "the lost stream's error must reach the consumer")
	case <-gotime.After(5 * gotime.Second):
		t.Fatal("the lost stream was not reported to the consumer")
	}

	assert.Eventually(t, func() bool {
		return srv.count("Watch") >= 3 && logs.FilterMessageSnippet("re-establish watch stream").Len() >= 2
	}, 5*gotime.Second, 20*gotime.Millisecond, "a rejected reconnect must be retried, not given up on")

	assertDrained(t, "while the reconnect retries", doc, peerID)
	require.NoError(t, cli.Detach(context.Background(), doc))
	drainUntilClosed(t, "after Detach", rch)
	assertUndrained(t, "after Detach", doc, peerID)
}

// TestWatchReconnectSkippedWhileDeactivating pins that a stream lost after
// Deactivate has left statusActivated is not re-established: the session is
// being ended, and a new Watch for it is what the deactivating window rejects
// everywhere else. Deactivate is parked on syncMu, so the watch context is
// still live when the stream drops.
func TestWatchReconnectSkippedWhileDeactivating(t *testing.T) {
	srv := newLifecycleServer()
	cli := dialLifecycle(t, srv)

	doc := document.New(key.Key("reconnect-deactivating"))
	require.NoError(t, cli.Attach(context.Background(), doc, WithRealtimeSync()))
	attachment, ok := cli.attachments.Get(doc.Key())
	require.True(t, ok)
	rch, _, err := cli.WatchStream(doc)
	require.NoError(t, err)

	attachment.syncMu.Lock()
	deactivated := make(chan error, 1)
	go func() { deactivated <- cli.Deactivate(context.Background()) }()
	waitFor(t, "Deactivate to leave statusActivated", func() bool {
		return cli.loadStatus() == statusDeactivating
	})

	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for range rch { //nolint:revive // drain until the reader closes the buffer
		}
	}()
	close(srv.failStream)

	// syncMu is still held, so Deactivate has not cancelled the watch context:
	// only the reader's own status check can end the stream here.
	endedQuietly := false
	select {
	case <-closed:
		endedQuietly = true
	case <-gotime.After(2 * gotime.Second):
	}
	attachment.syncMu.Unlock()

	assert.True(t, endedQuietly, "the lost stream was not ended while deactivating")
	assert.Equal(t, 1, srv.count("Watch"), "a deactivating client re-opened its watch stream")
	assert.NoError(t, receive(t, "Deactivate", deactivated))
}

// TestConcurrentDetachAndDeactivate races the two teardowns of one attachment
// under the race detector. Whichever wins, both return, the pipeline is
// retired exactly once, and the loser reports the attachment gone or the
// client inactive.
func TestConcurrentDetachAndDeactivate(t *testing.T) {
	srv := newLifecycleServer()
	cli := dialLifecycle(t, srv)

	for i := range 10 {
		doc := document.New(key.Key("concurrent-teardown"))
		peerID := givePeerPresence(t, doc)
		require.NoError(t, cli.Attach(context.Background(), doc, WithRealtimeSync()))

		var wg sync.WaitGroup
		var detachErr atomic.Value
		wg.Go(func() {
			if err := cli.Detach(context.Background(), doc); err != nil {
				detachErr.Store(err)
			}
		})
		wg.Go(func() { assert.NoError(t, cli.Deactivate(context.Background())) })
		finished := make(chan struct{})
		go func() {
			wg.Wait()
			close(finished)
		}()
		receive(t, "both teardowns", finished)

		if err, ok := detachErr.Load().(error); ok {
			assert.True(t, errors.Is(err, ErrNotActivated) || errors.Is(err, ErrNotAttached),
				"iteration %d: unexpected Detach error %v", i, err)
		}
		assert.Equal(t, attachable.StatusDetached, doc.Status())
		assertUndrained(t, "after both teardowns", doc, peerID)

		require.NoError(t, cli.Activate(context.Background()))
	}
}

// TestBeginAttachRetiresAStaleAttachment pins that dropping a stale entry to
// reuse its key also retires that entry's pipeline. Every other teardown does,
// and a pump left behind would keep draining a document no attachment holds.
func TestBeginAttachRetiresAStaleAttachment(t *testing.T) {
	srv := newLifecycleServer()
	cli := dialLifecycle(t, srv)

	doc := document.New(key.Key("stale-entry"))
	peerID := givePeerPresence(t, doc)
	watchCtx, closeWatch := context.WithCancel(context.Background())
	t.Cleanup(closeWatch)
	stale := &Attachment{
		resourceID:       types.ID("000000000000000000000002"),
		resource:         doc,
		watchCtx:         watchCtx,
		closeWatchStream: closeWatch,
	}
	startWatchPipeline(watchCtx, stale, doc)
	cli.attachments.Set(doc.Key(), stale)
	assertDrained(t, "the stale pipeline", doc, peerID)

	// The resource is not attached, which is what makes the entry stale.
	require.NoError(t, cli.beginAttach(doc.Key()))
	cli.endAttach(doc.Key())

	assert.Zero(t, cli.attachments.Len())
	assertUndrained(t, "after beginAttach dropped the entry", doc, peerID)
}

// TestDeactivateEndsChannelWatch pins that a channel's WatchChannel stream
// does not outlive the session it was opened under: Deactivate retires every
// attachment, channels included, and the watch is tied to the attachment.
func TestDeactivateEndsChannelWatch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		teardown func(cli *Client, ch *channel.Channel) error
	}{
		{"deactivate", func(cli *Client, _ *channel.Channel) error {
			return cli.Deactivate(context.Background())
		}},
		{"detach", func(cli *Client, ch *channel.Channel) error {
			return cli.Detach(context.Background(), ch)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := newLifecycleServer()
			cli := dialLifecycle(t, srv)

			ch, err := channel.New(key.Key("channel-watch-" + tc.name))
			require.NoError(t, err)
			require.NoError(t, cli.Attach(context.Background(), ch))
			counts, closeWatch, err := cli.WatchChannel(context.Background(), ch)
			require.NoError(t, err)
			t.Cleanup(closeWatch)

			require.NoError(t, tc.teardown(cli, ch))

			timeout := gotime.After(5 * gotime.Second)
			for open := true; open; {
				select {
				case _, open = <-counts:
				case <-timeout:
					t.Fatal("the channel watch outlived the teardown")
				}
			}
		})
	}
}

// TestWatchChannelRejectsSecondWatch pins the guard the broadcast-serving claim
// assumes: two watches of the same channel would read from one request queue,
// and the first to retire would retire the other's servicer with it, leaving a
// live watch whose every Broadcast reports ErrBroadcastUnavailable.
func TestWatchChannelRejectsSecondWatch(t *testing.T) {
	srv := newLifecycleServer()
	cli := dialLifecycle(t, srv)
	t.Cleanup(func() { _ = cli.Deactivate(context.Background()) })

	ch, err := channel.New(key.Key("channel-double-watch"))
	require.NoError(t, err)
	require.NoError(t, cli.Attach(context.Background(), ch))

	_, closeWatch, err := cli.WatchChannel(context.Background(), ch)
	require.NoError(t, err)

	_, _, err = cli.WatchChannel(context.Background(), ch)
	assert.ErrorIs(t, err, ErrAlreadyWatching)

	// Closing the first watch releases the claim, so the channel is watchable
	// again without waiting for the retiring servicer to wind down.
	closeWatch()
	_, closeSecond, err := cli.WatchChannel(context.Background(), ch)
	require.NoError(t, err)
	t.Cleanup(closeSecond)

	// The second watch owns the claim: its broadcasts reach the server -- this
	// one fails there, since lifecycleServer answers no Broadcast -- rather
	// than being refused by the first watch's teardown.
	assert.NotErrorIs(t, ch.Broadcast("topic", "payload"), channel.ErrBroadcastUnavailable)
}
