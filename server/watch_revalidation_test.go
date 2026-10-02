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

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/key"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// webhookPolicy is a synthetic auth webhook whose answer per token can be
// changed while streams are open.
type webhookPolicy struct {
	mu      sync.Mutex
	denied  map[string]bool
	failing map[string]bool
}

func (p *webhookPolicy) set(token string, denied, failing bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.denied[token] = denied
	p.failing[token] = failing
}

func (p *webhookPolicy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := types.NewAuthWebhookRequest(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	p.mu.Lock()
	denied, failing := p.denied[req.Token], p.failing[req.Token]
	p.mu.Unlock()

	switch {
	case failing:
		w.WriteHeader(http.StatusInternalServerError)
	case denied:
		w.WriteHeader(http.StatusForbidden)
		_, _ = (&types.AuthWebhookResponse{Allowed: false, Reason: "revoked"}).Write(w)
	default:
		_, _ = (&types.AuthWebhookResponse{Allowed: true}).Write(w)
	}
}

// openStream is a Watch-family stream drained in the background.
type openStream struct {
	broadcasts chan string
	ended      chan error
}

func drain[T any](stream *connect.ServerStreamForClient[T], payload func(*T) []byte) *openStream {
	s := &openStream{broadcasts: make(chan string, 16), ended: make(chan error, 1)}
	go func() {
		for stream.Receive() {
			if p := payload(stream.Msg()); p != nil {
				s.broadcasts <- string(p)
			}
		}
		s.ended <- stream.Err()
	}()
	return s
}

func (s *openStream) requireEndedWith(t *testing.T, code connect.Code) {
	t.Helper()
	select {
	case err := <-s.ended:
		require.Equal(t, code, connect.CodeOf(err), "stream ended with %v", err)
	case <-time.After(time.Second):
		t.Fatal("stream is still open after the revalidation")
	}
}

// requireReceived waits for the given broadcast, skipping earlier ones.
func (s *openStream) requireReceived(t *testing.T, payload string) {
	t.Helper()
	timeout := time.After(time.Second)
	for {
		select {
		case p := <-s.broadcasts:
			if p == payload {
				return
			}
		case <-timeout:
			t.Fatalf("stream did not receive %q", payload)
		}
	}
}

// requireOpen asserts the stream does not end within a short grace period,
// so a close already in flight is caught too.
func (s *openStream) requireOpen(t *testing.T) {
	t.Helper()
	select {
	case err := <-s.ended:
		t.Fatalf("stream ended unexpectedly: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestWatchAccessRevalidation(t *testing.T) {
	policy := &webhookPolicy{denied: map[string]bool{}, failing: map[string]bool{}}
	authServer := httptest.NewServer(policy)
	defer authServer.Close()

	conf := helper.TestConfig()
	conf.Mongo = nil
	svr, err := server.New(conf)
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { require.NoError(t, svr.Shutdown(true)) }()

	ctx := context.Background()
	admin := helper.CreateAdminCli(t, svr.RPCAddr())
	defer admin.Close()
	project, err := admin.CreateProject(ctx, "watch-revalidation")
	require.NoError(t, err)
	methods := []string{
		string(types.ActivateClient), string(types.Broadcast),
		string(types.Watch), string(types.WatchDocument), string(types.WatchChannel),
	}
	var noRetries uint64
	_, err = admin.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		AuthWebhookURL:        &authServer.URL,
		AuthWebhookMethods:    &methods,
		AuthWebhookMaxRetries: &noRetries,
	})
	require.NoError(t, err)

	type user struct {
		cli      v1connect.YorkieServiceClient
		clientID string
	}
	newUser := func(token string) user {
		cli := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+svr.RPCAddr(),
			connect.WithInterceptors(client.NewAuthInterceptor(project.PublicKey, token)))
		res, err := cli.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{ClientKey: token}))
		require.NoError(t, err)
		return user{cli: cli, clientID: res.Msg.ClientId}
	}
	watch := func(u user, channel string) *openStream {
		stream, err := u.cli.Watch(ctx, connect.NewRequest(&api.WatchRequest{
			ClientId: u.clientID,
			Resources: []*api.ResourceDescriptor{{Resource: &api.ResourceDescriptor_Channel{
				Channel: &api.ChannelDescriptor{ChannelKey: channel},
			}}},
		}))
		require.NoError(t, err)
		require.True(t, stream.Receive(), "Watch did not initialize: %v", stream.Err())
		return drain(stream, func(res *api.WatchResponse) []byte {
			if ev := res.GetEvent().GetChannelEvent().GetEvent(); ev.GetType() == api.ChannelEvent_TYPE_BROADCAST {
				return ev.Payload
			}
			return nil
		})
	}
	watchChannel := func(u user, channel string) *openStream {
		//nolint:staticcheck // Revalidation must cover the deprecated RPC while clients still use it.
		stream, err := u.cli.WatchChannel(ctx, connect.NewRequest(&api.WatchChannelRequest{
			ClientId: u.clientID, ChannelKey: channel,
		}))
		require.NoError(t, err)
		require.True(t, stream.Receive(), "WatchChannel did not initialize: %v", stream.Err())
		//nolint:staticcheck // The deprecated response is what WatchChannel returns.
		return drain(stream, func(res *api.WatchChannelResponse) []byte {
			if ev := res.GetEvent(); ev.GetType() == api.ChannelEvent_TYPE_BROADCAST {
				return ev.Payload
			}
			return nil
		})
	}
	watchDocument := func(u user, docKey string) *openStream {
		attached, err := u.cli.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
			ClientId:   u.clientID,
			ChangePack: &api.ChangePack{DocumentKey: docKey, Checkpoint: &api.Checkpoint{}},
		}))
		require.NoError(t, err)
		//nolint:staticcheck // Revalidation must cover the deprecated RPC while clients still use it.
		stream, err := u.cli.WatchDocument(ctx, connect.NewRequest(&api.WatchDocumentRequest{
			ClientId: u.clientID, DocumentId: attached.Msg.DocumentId,
		}))
		require.NoError(t, err)
		require.True(t, stream.Receive(), "WatchDocument did not initialize: %v", stream.Err())
		//nolint:staticcheck // The deprecated response is what WatchDocument returns.
		return drain(stream, func(*api.WatchDocumentResponse) []byte { return nil })
	}
	broadcast := func(u user, channel, payload string) error {
		_, err := u.cli.Broadcast(ctx, connect.NewRequest(&api.BroadcastRequest{
			ClientId: u.clientID, ChannelKey: channel, Topic: "t", Payload: []byte(payload),
		}))
		return err
	}

	alice, bob, carol, dave := newUser("alice"), newUser("bob"), newUser("carol"), newUser("dave")
	// Caches alice's allow for Broadcast, as any active client would have.
	require.NoError(t, broadcast(alice, "room-1", "warm-up"))
	aliceWatch := watch(alice, "room-1")
	aliceLegacy := watchChannel(alice, "room-1")
	aliceDoc := watchDocument(alice, "doc-1")
	bobWatch := watch(bob, "room-1")
	carolWatch := watch(carol, "room-2")

	t.Run("revoked streams close right after the call", func(t *testing.T) {
		policy.set("alice", true, false)
		policy.set("carol", true, false)

		// Without a revalidation the cached allow still admits alice: the
		// revocation has not reached this node yet.
		require.NoError(t, broadcast(alice, "room-1", "before"))
		require.NoError(t, broadcast(dave, "room-1", "leaked"))
		aliceWatch.requireReceived(t, "leaked")

		closed, err := admin.RevalidateAccess(ctx, project.Name, []key.Key{"room-1", "doc-1"})
		require.NoError(t, err)
		require.Equal(t, 3, closed)

		aliceWatch.requireEndedWith(t, connect.CodePermissionDenied)
		aliceLegacy.requireEndedWith(t, connect.CodePermissionDenied)
		aliceDoc.requireEndedWith(t, connect.CodePermissionDenied)
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(broadcast(alice, "room-1", "after")),
			"the cached allow outlived the revalidation")
	})

	t.Run("allowed and uncovered streams stay open", func(t *testing.T) {
		bobWatch.requireOpen(t)
		carolWatch.requireOpen(t) // revoked, but room-2 was not revalidated

		// A publisher does not receive its own broadcast, so dave sends it.
		require.NoError(t, broadcast(dave, "room-1", "still-delivered"))
		bobWatch.requireReceived(t, "still-delivered")
	})

	t.Run("an unanswered check closes the stream as retryable", func(t *testing.T) {
		policy.set("bob", false, true)

		closed, err := admin.RevalidateAccess(ctx, project.Name, nil)
		require.NoError(t, err)
		require.Equal(t, 2, closed)

		bobWatch.requireEndedWith(t, connect.CodeUnavailable)
		carolWatch.requireEndedWith(t, connect.CodePermissionDenied)
	})
}
