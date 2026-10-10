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
	"sync"
	"sync/atomic"
	"testing"
	gotime "time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/converter"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
)

// pushRejectServer answers PushPull for every document but one, which it
// refuses as over the size limit for as long as reject is set -- the answer
// a server gives no matter how often the same pack is resent.
type pushRejectServer struct {
	*watchInitServer

	rejectKey string
	reject    atomic.Bool
	// unavailable answers the reject key with a transient error instead,
	// which the client should retry after RetrySyncLoopDelay.
	unavailable bool

	mu      sync.Mutex
	docIDs  map[string]string
	pushes  map[string]int
	lastSeq map[string]int64
}

func newPushRejectServer(rejectKey string) *pushRejectServer {
	s := &pushRejectServer{
		watchInitServer: &watchInitServer{
			release: make(chan struct{}),
			firstResponse: &api.WatchResponse{
				Body: &api.WatchResponse_Initialization{
					Initialization: &api.WatchInitialization{},
				},
			},
		},
		rejectKey: rejectKey,
		docIDs:    map[string]string{},
		pushes:    map[string]int{},
		lastSeq:   map[string]int64{},
	}
	s.reject.Store(true)
	return s
}

func (s *pushRejectServer) AttachDocument(
	_ context.Context,
	req *connect.Request[api.AttachDocumentRequest],
) (*connect.Response[api.AttachDocumentResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	docKey := req.Msg.ChangePack.DocumentKey
	docID := "00000000000000000000000" + string(rune('a'+len(s.docIDs)))
	s.docIDs[docID] = docKey

	return connect.NewResponse(&api.AttachDocumentResponse{
		DocumentId: docID,
		ChangePack: &api.ChangePack{
			DocumentKey:   docKey,
			Checkpoint:    &api.Checkpoint{ServerSeq: 1, ClientSeq: 1},
			VersionVector: req.Msg.ChangePack.VersionVector,
		},
	}), nil
}

func (s *pushRejectServer) PushPullChanges(
	_ context.Context,
	req *connect.Request[api.PushPullChangesRequest],
) (*connect.Response[api.PushPullChangesResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	pack := req.Msg.ChangePack
	s.pushes[pack.DocumentKey]++

	if pack.DocumentKey == s.rejectKey && s.reject.Load() && len(pack.Changes) > 0 {
		if s.unavailable {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("server is restarting"))
		}
		return nil, connecthelper.ToConnectError(document.ErrDocumentSizeExceedsLimit)
	}

	seq := s.lastSeq[pack.DocumentKey] + int64(len(pack.Changes)) + 1
	s.lastSeq[pack.DocumentKey] = seq
	clientSeq := pack.Checkpoint.ClientSeq
	if n := len(pack.Changes); n > 0 {
		clientSeq = pack.Changes[n-1].Id.ClientSeq
	}

	return connect.NewResponse(&api.PushPullChangesResponse{
		ChangePack: &api.ChangePack{
			DocumentKey:   pack.DocumentKey,
			Checkpoint:    &api.Checkpoint{ServerSeq: seq, ClientSeq: clientSeq},
			VersionVector: pack.VersionVector,
		},
	}), nil
}

func (s *pushRejectServer) pushesOf(docKey string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pushes[docKey]
}

// TestSyncLoopParksRejectedPush pins what the realtime sync loop does with a
// push the server refuses outright: it stops resending that document's pack
// for RejectedPushRetryDelay, it keeps syncing the client's other documents
// without waiting on the refused one, and an explicit Sync that goes through
// puts the document back in the loop without waiting the delay out.
func TestSyncLoopParksRejectedPush(t *testing.T) {
	ctx := context.Background()
	rejected := key.Key("rejected-doc")
	other := key.Key("other-doc")

	srv := newPushRejectServer(rejected.String())
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(srv.release)
		httpServer.Close()
	})

	cli, err := client.Dial(httpServer.URL,
		client.WithSyncLoopDuration(10*gotime.Millisecond),
		client.WithRetrySyncLoopDelay(gotime.Second),
		client.WithRejectedPushRetryDelay(30*gotime.Second),
	)
	require.NoError(t, err)
	require.NoError(t, cli.Activate(ctx))
	t.Cleanup(func() { _ = cli.Close() })

	d1 := document.New(rejected)
	d2 := document.New(other)
	require.NoError(t, cli.Attach(ctx, d1, client.WithRealtimeSync()))
	require.NoError(t, cli.Attach(ctx, d2, client.WithRealtimeSync()))

	update := func(d *document.Document, v string) {
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString("k", v)
			return nil
		}))
	}

	// The refused document is pushed once and then left alone for the delay.
	update(d1, "too big")
	assert.Eventually(t, func() bool { return srv.pushesOf(rejected.String()) >= 1 },
		gotime.Second, 5*gotime.Millisecond)
	gotime.Sleep(2500 * gotime.Millisecond)
	assert.Equal(t, 1, srv.pushesOf(rejected.String()), "a refused pack is resent")

	// The client's other document still syncs within a few rounds, not
	// behind a retry delay the refused document keeps restarting.
	before := srv.pushesOf(other.String())
	update(d2, "edit")
	start := gotime.Now()
	assert.Eventually(t, func() bool { return srv.pushesOf(other.String()) > before },
		2*gotime.Second, 5*gotime.Millisecond)
	assert.Less(t, gotime.Since(start), 300*gotime.Millisecond,
		"another document waits on the refused one")

	// An explicit Sync still reports the refusal to its caller.
	err = cli.Sync(ctx, client.WithKey(rejected))
	assert.Equal(t, document.ErrDocumentSizeExceedsLimit.Code(), converter.ErrorCodeOf(err))

	// Once a Sync goes through, the loop picks the document up again.
	srv.reject.Store(false)
	require.NoError(t, cli.Sync(ctx, client.WithKey(rejected)))
	pushed := srv.pushesOf(rejected.String())
	update(d1, "smaller")
	assert.Eventually(t, func() bool { return srv.pushesOf(rejected.String()) > pushed },
		gotime.Second, 5*gotime.Millisecond, "the loop did not resume the document")
}

// TestSyncLoopReprobesRejectedPush pins that the hold-off a refused push gets
// ends by itself: the loop re-probes once RejectedPushRetryDelay has passed,
// so a document the server would now accept -- the limit raised, or peers
// having shrunk it, neither of which this client is party to -- syncs again
// with no explicit Sync from the app. The probe is one push per delay, not
// one per round.
func TestSyncLoopReprobesRejectedPush(t *testing.T) {
	ctx := context.Background()
	rejected := key.Key("rejected-doc")

	srv := newPushRejectServer(rejected.String())
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(srv.release)
		httpServer.Close()
	})

	cli, err := client.Dial(httpServer.URL,
		client.WithSyncLoopDuration(10*gotime.Millisecond),
		client.WithRetrySyncLoopDelay(gotime.Second),
		client.WithRejectedPushRetryDelay(500*gotime.Millisecond),
	)
	require.NoError(t, err)
	require.NoError(t, cli.Activate(ctx))
	t.Cleanup(func() { _ = cli.Close() })

	d := document.New(rejected)
	require.NoError(t, cli.Attach(ctx, d, client.WithRealtimeSync()))
	require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
		r.SetString("k", "too big")
		return nil
	}))

	assert.Eventually(t, func() bool { return srv.pushesOf(rejected.String()) >= 1 },
		gotime.Second, 5*gotime.Millisecond)

	// Over four delays the document is probed a handful of times, not on
	// every one of the ~200 rounds they hold.
	gotime.Sleep(2 * gotime.Second)
	n := srv.pushesOf(rejected.String())
	assert.GreaterOrEqual(t, n, 2, "a refused document is never probed again")
	assert.LessOrEqual(t, n, 8, "a refused document is probed every round")

	// Once the server would accept it, the probe goes through on its own.
	srv.reject.Store(false)
	assert.Eventually(t, func() bool { return !d.HasLocalChanges() },
		3*gotime.Second, 10*gotime.Millisecond,
		"the loop never recovered the document by itself")
}

// TestSyncLoopRetriesFailedSyncAlone pins that a transient failure holds back
// only the attachment it happened to: the document is retried after
// RetrySyncLoopDelay rather than every round, and the client's other
// documents keep syncing in the meantime instead of waiting out the delay.
func TestSyncLoopRetriesFailedSyncAlone(t *testing.T) {
	ctx := context.Background()
	failing := key.Key("failing-doc")
	other := key.Key("other-doc")

	srv := newPushRejectServer(failing.String())
	srv.unavailable = true
	mux := http.NewServeMux()
	mux.Handle(v1connect.NewYorkieServiceHandler(srv))
	httpServer := httptest.NewServer(mux)
	t.Cleanup(func() {
		close(srv.release)
		httpServer.Close()
	})

	cli, err := client.Dial(httpServer.URL,
		client.WithSyncLoopDuration(10*gotime.Millisecond),
		client.WithRetrySyncLoopDelay(gotime.Second),
	)
	require.NoError(t, err)
	require.NoError(t, cli.Activate(ctx))
	t.Cleanup(func() { _ = cli.Close() })

	d1 := document.New(failing)
	d2 := document.New(other)
	require.NoError(t, cli.Attach(ctx, d1, client.WithRealtimeSync()))
	require.NoError(t, cli.Attach(ctx, d2, client.WithRealtimeSync()))

	update := func(d *document.Document, v string) {
		require.NoError(t, d.Update(func(r *json.Object, _ *presence.Presence) error {
			r.SetString("k", v)
			return nil
		}))
	}

	update(d1, "first")
	assert.Eventually(t, func() bool { return srv.pushesOf(failing.String()) >= 1 },
		gotime.Second, 5*gotime.Millisecond)

	// Another document goes out within a few rounds of the failure.
	before := srv.pushesOf(other.String())
	update(d2, "edit")
	start := gotime.Now()
	assert.Eventually(t, func() bool { return srv.pushesOf(other.String()) > before },
		2*gotime.Second, 5*gotime.Millisecond)
	assert.Less(t, gotime.Since(start), 300*gotime.Millisecond,
		"another document waits out the failing one's retry delay")

	// The failing document is retried, once per delay rather than per round.
	gotime.Sleep(2200 * gotime.Millisecond)
	n := srv.pushesOf(failing.String())
	assert.GreaterOrEqual(t, n, 2, "the failing document is not retried")
	assert.LessOrEqual(t, n, 4, "the failing document is retried every round")

	// It catches up once the server answers again.
	srv.reject.Store(false)
	assert.Eventually(t, func() bool { return !d1.HasLocalChanges() },
		3*gotime.Second, 10*gotime.Millisecond)
}
