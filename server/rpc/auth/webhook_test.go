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

package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
	backendcache "github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// newWebhookFixture returns a project pointed at a webhook whose verdict the
// returned flag controls, alongside the backend that calls it and a counter of
// the requests it actually received.
func newWebhookFixture(t *testing.T) (*backend.Backend, *types.Project, *atomic.Bool, *atomic.Int64) {
	t.Helper()

	var allowed atomic.Bool
	allowed.Store(true)
	var calls atomic.Int64
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if !allowed.Load() {
			w.WriteHeader(http.StatusForbidden)
		}
		_, _ = (&types.AuthWebhookResponse{Allowed: allowed.Load()}).Write(w)
	}))
	t.Cleanup(hook.Close)

	caches, err := backendcache.New(backendcache.Options{
		AuthWebhookCacheSize: 16, AuthWebhookCacheTTL: time.Minute,
		SnapshotCacheSize: 1, ChannelSessionCountCacheSize: 1, ChannelSessionCountCacheTTL: time.Second,
	})
	require.NoError(t, err)
	be := &backend.Backend{
		Cache:             caches,
		AuthWebhookClient: webhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse](false),
	}
	t.Cleanup(be.AuthWebhookClient.Close)

	return be, &types.Project{
		PublicKey:                  "cache-policy-project",
		AuthWebhookURL:             hook.URL,
		AuthWebhookMethods:         []string{string(types.Watch), string(types.PushPull)},
		AuthWebhookMaxRetries:      0,
		AuthWebhookMinWaitInterval: "1ms",
		AuthWebhookMaxWaitInterval: "1ms",
		AuthWebhookRequestTimeout:  "3s",
	}, &allowed, &calls
}

// An ordinary RPC is amortized by the cache; a Watch is not admitted by a
// cached allow, because the stream it opens outlives the question.
func TestCachedAllowIsNotServedToWatch(t *testing.T) {
	be, project, allowed, calls := newWebhookFixture(t)
	ctx := context.Background()
	pushPull := &types.AccessInfo{Method: types.PushPull}
	watch := &types.AccessInfo{Method: types.Watch}

	require.NoError(t, verifyAccessWithCache(ctx, be, project, "token", pushPull, cacheAnyDecision))
	require.Equal(t, int64(1), calls.Load())
	require.NoError(t, verifyAccessWithCache(ctx, be, project, "token", pushPull, cacheAnyDecision))
	require.Equal(t, int64(1), calls.Load(), "an ordinary RPC must be answered from the cache")

	require.NoError(t, verifyAccessWithCache(ctx, be, project, "token", watch, cacheDenialsOnly))
	require.Equal(t, int64(2), calls.Load())
	require.NoError(t, verifyAccessWithCache(ctx, be, project, "token", watch, cacheDenialsOnly))
	require.Equal(t, int64(3), calls.Load(), "a cached allow must not admit a Watch")

	// A revocation the cached allow still hides is observed by the next check.
	allowed.Store(false)
	require.ErrorIs(t, verifyAccessWithCache(ctx, be, project, "token", watch, cacheDenialsOnly),
		ErrPermissionDenied)
	require.Equal(t, int64(4), calls.Load())
}

// A cached denial still answers on the spot, so a client retrying a refused
// Watch is not amplified into the project's webhook.
func TestCachedDenialIsServedToWatch(t *testing.T) {
	be, project, allowed, calls := newWebhookFixture(t)
	ctx := context.Background()
	watch := &types.AccessInfo{Method: types.Watch}
	allowed.Store(false)

	for range 5 {
		err := verifyAccessWithCache(ctx, be, project, "token", watch, cacheDenialsOnly)
		require.ErrorIs(t, err, ErrPermissionDenied)
		require.True(t, isDenial(err))
	}
	require.Equal(t, int64(1), calls.Load(), "repeated denied attempts must not reach the webhook")
}

// The lease asks the same question the admission did, so it must not become a
// second, divergent derivation of the cached decision.
func TestVerifyWatchLeaseSharesTheAdmissionDecision(t *testing.T) {
	be, project, allowed, calls := newWebhookFixture(t)
	ctx := metadata.With(context.Background(), metadata.Metadata{Authorization: "token"})
	watch := &types.AccessInfo{Method: types.Watch}

	require.NoError(t, VerifyWatchLease(ctx, be, project, watch))
	require.Equal(t, int64(1), calls.Load())

	// The lease's observation of a revocation replaces the allow the other
	// RPCs would otherwise keep reading until the TTL elapsed.
	allowed.Store(false)
	require.ErrorIs(t, VerifyWatchLease(ctx, be, project, watch), ErrPermissionDenied)
	require.Equal(t, int64(2), calls.Load())

	err := verifyAccessWithCache(ctx, be, project, "token", watch, cacheAnyDecision)
	require.True(t, errors.IsStatus(err, errors.ErrCodePermissionDenied))
	require.Equal(t, int64(2), calls.Load(), "the lease decision must be written back to the cache")

	// A project that protects nothing is not asked about at all.
	unprotected := *project
	unprotected.AuthWebhookURL = ""
	require.NoError(t, VerifyWatchLease(ctx, be, &unprotected, watch))
	require.Equal(t, int64(2), calls.Load())
}
