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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/backend/cache"
)

// webhookStub answers every request with the given status and counts calls.
type webhookStub struct {
	status atomic.Int32
	calls  atomic.Int32
}

func (s *webhookStub) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.calls.Add(1)
	status := int(s.status.Load())
	w.WriteHeader(status)
	_, _ = (&types.AuthWebhookResponse{Allowed: status == http.StatusOK}).Write(w)
}

func newWebhookTest(t *testing.T) (*backend.Backend, *types.Project, *webhookStub) {
	stub := &webhookStub{}
	stub.status.Store(http.StatusOK)
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)

	caches, err := cache.New(cache.Options{
		AuthWebhookCacheSize:         100,
		AuthWebhookCacheTTL:          time.Minute,
		SnapshotCacheSize:            1,
		ChannelSessionCountCacheSize: 1,
		ChannelSessionCountCacheTTL:  time.Minute,
	})
	require.NoError(t, err)
	client := webhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse](false)
	t.Cleanup(client.Close)

	project := &types.Project{
		PublicKey:                  "public-key",
		AuthWebhookURL:             srv.URL,
		AuthWebhookMethods:         []string{string(types.Watch)},
		AuthWebhookMaxRetries:      10,
		AuthWebhookMinWaitInterval: "1ms",
		AuthWebhookMaxWaitInterval: "1ms",
		AuthWebhookRequestTimeout:  "1s",
	}
	return &backend.Backend{Cache: caches, AuthWebhookClient: client}, project, stub
}

func watchOf(keys ...string) *types.AccessInfo {
	attrs := make([]types.AccessAttribute, len(keys))
	for i, k := range keys {
		attrs[i] = types.AccessAttribute{Key: k, Verb: types.Read}
	}
	return &types.AccessInfo{Method: types.Watch, Attributes: attrs}
}

func TestRecheckAccess(t *testing.T) {
	ctx := context.Background()

	t.Run("ignores a cached allow that predates the change", func(t *testing.T) {
		be, project, stub := newWebhookTest(t)
		access := watchOf("doc-1")

		// An allow is cached under the old policy, then the policy changes.
		require.NoError(t, verifyAccess(ctx, be, project, "alice", access, false))
		stub.status.Store(http.StatusForbidden)
		require.NoError(t, verifyAccess(ctx, be, project, "alice", access, false),
			"an ordinary check is still served from the cache")

		err := RecheckAccess(ctx, be, project, "alice", access)

		assert.ErrorIs(t, err, ErrPermissionDenied)
		assert.ErrorIs(t, verifyAccess(ctx, be, project, "alice", access, false), ErrPermissionDenied,
			"the recheck's answer replaces the stale one in the cache")
	})

	t.Run("asks once instead of using the project's retries", func(t *testing.T) {
		be, project, stub := newWebhookTest(t)
		stub.status.Store(http.StatusServiceUnavailable)

		err := RecheckAccess(ctx, be, project, "alice", watchOf("doc-1"))

		assert.Error(t, err)
		assert.Equal(t, int32(1), stub.calls.Load())
	})

	t.Run("skips methods the project does not protect", func(t *testing.T) {
		be, project, stub := newWebhookTest(t)
		project.AuthWebhookMethods = nil

		assert.NoError(t, RecheckAccess(ctx, be, project, "alice", watchOf("doc-1")))
		assert.Equal(t, int32(0), stub.calls.Load())
	})
}

func TestDropCachedDecisions(t *testing.T) {
	be, project, _ := newWebhookTest(t)
	other := *project
	other.PublicKey = "other-key"
	allow := pkgtypes.Pair[int, *types.AuthWebhookResponse]{
		First: http.StatusOK, Second: &types.AuthWebhookResponse{Allowed: true},
	}
	cacheKey := func(prj *types.Project, token string, keys ...string) string {
		access := watchOf(keys...)
		// The same encoding verifyAccess caches under.
		body, err := json.Marshal(types.AuthWebhookRequest{
			Token: token, Method: access.Method, Attributes: access.Attributes,
		})
		require.NoError(t, err)
		return generateCacheKey(prj.PublicKey, body)
	}
	fill := func() {
		be.Cache.AuthWebhook.Purge()
		for _, k := range []string{
			cacheKey(project, "alice", "doc-1"),
			cacheKey(project, "alice", "doc-1", "room-1"),
			cacheKey(project, "alice", "doc-10"),
			cacheKey(&other, "alice", "doc-1"),
		} {
			be.Cache.AuthWebhook.Add(k, allow)
		}
	}

	t.Run("drops only decisions about the keys", func(t *testing.T) {
		fill()
		assert.Equal(t, 2, DropCachedDecisions(be, project, []string{"doc-1"}))
		assert.True(t, be.Cache.AuthWebhook.Contains(cacheKey(project, "alice", "doc-10")))
		assert.True(t, be.Cache.AuthWebhook.Contains(cacheKey(&other, "alice", "doc-1")))
	})

	t.Run("drops every decision of the project without keys", func(t *testing.T) {
		fill()
		assert.Equal(t, 3, DropCachedDecisions(be, project, nil))
		assert.True(t, be.Cache.AuthWebhook.Contains(cacheKey(&other, "alice", "doc-1")))
	})
}
