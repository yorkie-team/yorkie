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
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	api "github.com/yorkie-team/yorkie/api/yorkie/v1"
	"github.com/yorkie-team/yorkie/api/yorkie/v1/v1connect"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

// TestWatchStreamEndsAfterWebhookRevocation is a regression test for an
// authorization change made after the Watch RPC has already been accepted.
func TestWatchStreamEndsAfterWebhookRevocation(t *testing.T) {
	var allowed atomic.Bool
	allowed.Store(true)
	var watchChecks atomic.Int64
	var watchAttributesMu sync.Mutex
	var firstWatchAttributes []types.AccessAttribute
	var watchAttributesChanged bool
	var legacyChecks atomic.Int64
	var channelChecks atomic.Int64
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Method == types.Watch {
			watchChecks.Add(1)
			watchAttributesMu.Lock()
			attributes := append([]types.AccessAttribute(nil), req.Attributes...)
			if firstWatchAttributes == nil {
				firstWatchAttributes = attributes
			} else if !reflect.DeepEqual(firstWatchAttributes, attributes) {
				watchAttributesChanged = true
			}
			watchAttributesMu.Unlock()
		}
		if req.Method == types.WatchDocument {
			legacyChecks.Add(1)
		}
		if req.Method == types.WatchChannel {
			channelChecks.Add(1)
		}
		if !allowed.Load() && req.Method != types.Watch {
			// A webhook that cannot answer is also an uncertain lease. Idle
			// legacy streams must close within the same cutoff.
			<-r.Context().Done()
			return
		}
		if !allowed.Load() {
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = (&types.AuthWebhookResponse{Allowed: allowed.Load()}).Write(w)
	}))
	defer authServer.Close()

	conf := helper.TestConfig()
	conf.Mongo = nil
	// The lease re-checks once per configured auth webhook cache TTL, so the
	// revocation window this test asserts is the operator's own setting rather
	// than a hardcoded server policy.
	conf.Backend.AuthWebhookCacheTTL = "1s"
	svr, err := server.New(conf)
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { require.NoError(t, svr.Shutdown(true)) }()

	ctx := context.Background()
	admin := helper.CreateAdminCli(t, svr.RPCAddr())
	defer admin.Close()
	project, err := admin.CreateProject(ctx, "watch-cutoff")
	require.NoError(t, err)
	methods := []string{string(types.Watch), string(types.WatchDocument), string(types.WatchChannel)}
	// A single attempt the project's own timeout allows always fits inside the
	// window, so the window is one re-check period plus that timeout.
	requestTimeout := "500ms"
	var noRetries uint64
	_, err = admin.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		AuthWebhookURL:            &authServer.URL,
		AuthWebhookMethods:        &methods,
		AuthWebhookRequestTimeout: &requestTimeout,
		AuthWebhookMaxRetries:     &noRetries,
	})
	require.NoError(t, err)

	apiClient := v1connect.NewYorkieServiceClient(
		http.DefaultClient,
		"http://"+svr.RPCAddr(),
		connect.WithInterceptors(client.NewAuthInterceptor(project.PublicKey, "synthetic-token")),
	)
	activated, err := apiClient.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{
		ClientKey: t.Name(),
	}))
	require.NoError(t, err)
	attached, err := apiClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
		ClientId: activated.Msg.ClientId,
		ChangePack: &api.ChangePack{
			DocumentKey: helper.TestKey(t).String(),
			Checkpoint:  &api.Checkpoint{},
		},
	}))
	require.NoError(t, err)

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	watchRequest := &api.WatchRequest{
		ClientId: activated.Msg.ClientId,
		Resources: []*api.ResourceDescriptor{{
			Resource: &api.ResourceDescriptor_Document{
				Document: &api.DocumentDescriptor{DocumentId: attached.Msg.DocumentId},
			},
		}, {
			Resource: &api.ResourceDescriptor_Channel{
				Channel: &api.ChannelDescriptor{ChannelKey: "unified-revocable-channel"},
			},
		}},
	}
	stream, err := apiClient.Watch(watchCtx, connect.NewRequest(watchRequest))
	require.NoError(t, err)
	require.True(t, stream.Receive(), "Watch did not send its initialization: %v", stream.Err())
	require.NotNil(t, stream.Msg().GetInitialization())
	require.Equal(t, int64(1), watchChecks.Load())
	//nolint:staticcheck // Revocation must cover the deprecated RPC while clients still use it.
	legacy, err := apiClient.WatchDocument(watchCtx, connect.NewRequest(&api.WatchDocumentRequest{
		ClientId: activated.Msg.ClientId, DocumentId: attached.Msg.DocumentId,
	}))
	require.NoError(t, err)
	require.True(t, legacy.Receive(), "legacy Watch did not initialize: %v", legacy.Err())
	require.NotNil(t, legacy.Msg().GetInitialization())
	require.Equal(t, int64(1), legacyChecks.Load())
	//nolint:staticcheck // Revocation must cover the deprecated RPC while clients still use it.
	channel, err := apiClient.WatchChannel(watchCtx, connect.NewRequest(&api.WatchChannelRequest{
		ClientId: activated.Msg.ClientId, ChannelKey: "revocable-channel",
	}))
	require.NoError(t, err)
	require.True(t, channel.Receive(), "legacy channel Watch did not initialize: %v", channel.Err())
	require.NotNil(t, channel.Msg().GetInitialized())
	require.Equal(t, int64(1), channelChecks.Load())
	ended := make(chan error, 1)
	legacyEnded := make(chan error, 1)
	channelEnded := make(chan error, 1)
	go func() {
		for stream.Receive() {
		}
		ended <- stream.Err()
	}()
	go func() {
		for legacy.Receive() {
		}
		legacyEnded <- legacy.Err()
	}()
	go func() {
		for channel.Receive() {
		}
		channelEnded <- channel.Err()
	}()

	// A permitted lease must renew for as long as the webhook keeps allowing
	// it, over many re-check periods.
	keepalive := time.NewTimer(4500 * time.Millisecond)
	defer keepalive.Stop()
	select {
	case err := <-ended:
		t.Fatalf("permitted Watch closed before revocation: %v", err)
	case err := <-legacyEnded:
		t.Fatalf("permitted WatchDocument closed before revocation: %v", err)
	case err := <-channelEnded:
		t.Fatalf("permitted WatchChannel closed before revocation: %v", err)
	case <-keepalive.C:
	}
	require.GreaterOrEqual(t, watchChecks.Load(), int64(3))
	require.GreaterOrEqual(t, legacyChecks.Load(), int64(3))
	require.GreaterOrEqual(t, channelChecks.Load(), int64(3))

	allowed.Store(false)
	cutoff := time.NewTimer(5 * time.Second)
	defer cutoff.Stop()
	// A distinct denied token proves the current webhook state without
	// disturbing the original token's cached allow. The old stream must end
	// even while that admission cache entry remains valid.
	freshClient := v1connect.NewYorkieServiceClient(
		http.DefaultClient,
		"http://"+svr.RPCAddr(),
		connect.WithInterceptors(client.NewAuthInterceptor(project.PublicKey, "revoked-token")),
	)
	freshCtx, cancelFresh := context.WithTimeout(ctx, 2*time.Second)
	defer cancelFresh()
	fresh, err := freshClient.Watch(freshCtx, connect.NewRequest(watchRequest))
	if err == nil {
		require.False(t, fresh.Receive(), "a new Watch succeeded after authorization was revoked")
		err = fresh.Err()
	}
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.GreaterOrEqual(t, watchChecks.Load(), int64(4), "revocation was not checked by the webhook")

	select {
	case err := <-ended:
		require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	case <-cutoff.C:
		cancel()
		t.Fatal("Watch remained open for 5 seconds after authorization was revoked")
	}
	select {
	case err := <-legacyEnded:
		require.Error(t, err)
	case <-cutoff.C:
		cancel()
		t.Fatal("legacy WatchDocument remained open for 5 seconds after revocation")
	}
	select {
	case err := <-channelEnded:
		require.Error(t, err)
	case <-cutoff.C:
		cancel()
		t.Fatal("legacy WatchChannel remained open for 5 seconds after revocation")
	}
	require.GreaterOrEqual(t, watchChecks.Load(), int64(3), "unified stream was not rechecked")
	require.GreaterOrEqual(t, legacyChecks.Load(), int64(2), "legacy stream was not rechecked")
	require.GreaterOrEqual(t, channelChecks.Load(), int64(2), "channel stream was not rechecked")
	watchAttributesMu.Lock()
	require.Len(t, firstWatchAttributes, 2)
	require.False(t, watchAttributesChanged, "Watch resource attributes changed during lease rechecks")
	watchAttributesMu.Unlock()
}

// A cached allow for the same credentials must not admit a revoked Watch.
func TestWatchAdmissionBypassesCachedAllowForSameCredentials(t *testing.T) {
	for _, method := range []types.Method{types.Watch, types.WatchDocument, types.WatchChannel} {
		t.Run(string(method), func(t *testing.T) {
			var allowed atomic.Bool
			allowed.Store(true)
			var checks atomic.Int64
			var requestMu sync.Mutex
			var firstRequest *types.AuthWebhookRequest
			var requestChanged bool
			hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req, err := types.NewAuthWebhookRequest(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				checks.Add(1)
				requestMu.Lock()
				if firstRequest == nil {
					firstRequest = req
				} else if !reflect.DeepEqual(firstRequest, req) {
					requestChanged = true
				}
				requestMu.Unlock()
				if !allowed.Load() {
					w.WriteHeader(http.StatusForbidden)
				}
				_, _ = (&types.AuthWebhookResponse{Allowed: allowed.Load()}).Write(w)
			}))
			defer hook.Close()
			conf := helper.TestConfig()
			conf.Mongo = nil
			conf.Backend.AuthWebhookCacheTTL = "1m"
			svr, err := server.New(conf)
			require.NoError(t, err)
			require.NoError(t, svr.Start())
			defer func() { require.NoError(t, svr.Shutdown(true)) }()
			ctx := context.Background()
			admin := helper.CreateAdminCli(t, svr.RPCAddr())
			defer admin.Close()
			project, err := admin.CreateProject(ctx, "watch-cache-admission")
			require.NoError(t, err)
			methods := []string{string(method)}
			_, err = admin.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
				AuthWebhookURL: &hook.URL, AuthWebhookMethods: &methods,
			})
			require.NoError(t, err)
			apiClient := v1connect.NewYorkieServiceClient(http.DefaultClient, "http://"+svr.RPCAddr(),
				connect.WithInterceptors(client.NewAuthInterceptor(project.PublicKey, "same-synthetic-token")))
			activated, err := apiClient.ActivateClient(ctx, connect.NewRequest(&api.ActivateClientRequest{ClientKey: t.Name()}))
			require.NoError(t, err)
			attached, err := apiClient.AttachDocument(ctx, connect.NewRequest(&api.AttachDocumentRequest{
				ClientId:   activated.Msg.ClientId,
				ChangePack: &api.ChangePack{DocumentKey: helper.TestKey(t).String(), Checkpoint: &api.Checkpoint{}},
			}))
			require.NoError(t, err)
			// Every attempt uses the same token, RPC method and resource request.
			open := func(watchCtx context.Context) (bool, error) {
				switch method {
				case types.Watch:
					stream, err := apiClient.Watch(watchCtx, connect.NewRequest(&api.WatchRequest{
						ClientId: activated.Msg.ClientId,
						Resources: []*api.ResourceDescriptor{{Resource: &api.ResourceDescriptor_Document{
							Document: &api.DocumentDescriptor{DocumentId: attached.Msg.DocumentId},
						}}},
					}))
					if err != nil {
						return false, err
					}
					defer func() { _ = stream.Close() }()
					received := stream.Receive()
					return received, stream.Err()
				case types.WatchDocument:
					//nolint:staticcheck // Admission must protect clients using the deprecated RPC.
					stream, err := apiClient.WatchDocument(watchCtx, connect.NewRequest(&api.WatchDocumentRequest{
						ClientId: activated.Msg.ClientId, DocumentId: attached.Msg.DocumentId,
					}))
					if err != nil {
						return false, err
					}
					defer func() { _ = stream.Close() }()
					received := stream.Receive()
					return received, stream.Err()
				default:
					//nolint:staticcheck // Admission must protect clients using the deprecated RPC.
					stream, err := apiClient.WatchChannel(watchCtx, connect.NewRequest(&api.WatchChannelRequest{
						ClientId: activated.Msg.ClientId, ChannelKey: "same-revocable-channel",
					}))
					if err != nil {
						return false, err
					}
					defer func() { _ = stream.Close() }()
					received := stream.Receive()
					return received, stream.Err()
				}
			}
			firstCtx, cancelFirst := context.WithTimeout(ctx, time.Second)
			received, err := open(firstCtx)
			cancelFirst()
			require.NoError(t, err)
			require.True(t, received)
			require.Equal(t, int64(1), checks.Load())
			requestMu.Lock()
			body, err := json.Marshal(firstRequest)
			requestMu.Unlock()
			require.NoError(t, err)
			cacheKey := fmt.Sprintf("%s:auth:%s", project.PublicKey, body)
			cached, ok := svr.Backend().Cache.AuthWebhook.Get(cacheKey)
			require.True(t, ok, "the same request must have a live cached allow before revocation")
			require.True(t, cached.Second.Allowed)
			allowed.Store(false)
			// No established stream remains to overwrite the cached allow on a lease tick.
			cached, ok = svr.Backend().Cache.AuthWebhook.Get(cacheKey)
			require.True(t, ok)
			require.True(t, cached.Second.Allowed)
			deniedCtx, cancelDenied := context.WithTimeout(ctx, time.Second)
			defer cancelDenied()
			received, err = open(deniedCtx)
			require.False(t, received, "cached allow admitted the revoked credentials")
			require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
			require.Equal(t, int64(2), checks.Load(), "re-admission must consult the webhook")
			requestMu.Lock()
			defer requestMu.Unlock()
			require.False(t, requestChanged, "re-admission changed the token, method or resource attributes")
		})
	}
}
