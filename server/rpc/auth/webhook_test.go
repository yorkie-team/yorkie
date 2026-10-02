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
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
	backendcache "github.com/yorkie-team/yorkie/server/backend/cache"
)

func TestAuthWebhookCacheDisabledAfterRevocation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		disabled   bool
		wantCalls  int64
		wantDenied bool
	}{
		{name: "explicitly disabled", disabled: true, wantCalls: 2, wantDenied: true},
		{name: "enabled cache serves the cached decision", wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var allowed atomic.Bool
			allowed.Store(true)
			var calls atomic.Int64
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, err := types.NewAuthWebhookRequest(r.Body)
				require.NoError(t, err)
				require.Equal(t, types.PushPull, request.Method)
				calls.Add(1)
				if !allowed.Load() {
					w.WriteHeader(http.StatusForbidden)
				}
				_, err = (&types.AuthWebhookResponse{Allowed: allowed.Load()}).Write(w)
				require.NoError(t, err)
			}))
			defer srv.Close()

			caches, err := backendcache.New(backendcache.Options{
				AuthWebhookCacheSize:         8,
				AuthWebhookCacheTTL:          time.Minute,
				SnapshotCacheSize:            8,
				ChannelSessionCountCacheSize: 8,
				ChannelSessionCountCacheTTL:  time.Minute,
			})
			require.NoError(t, err)
			client := webhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse](false)
			defer client.Close()
			be := &backend.Backend{
				Config:            &backend.Config{AuthWebhookCacheDisabled: tc.disabled},
				Cache:             caches,
				AuthWebhookClient: client,
			}
			project := &types.Project{
				PublicKey:                  "synthetic-project",
				AuthWebhookURL:             srv.URL,
				AuthWebhookMinWaitInterval: "1ms",
				AuthWebhookMaxWaitInterval: "1ms",
				AuthWebhookRequestTimeout:  "1s",
			}
			access := &types.AccessInfo{
				Method:     types.PushPull,
				Attributes: []types.AccessAttribute{{Key: "synthetic-document", Verb: types.Read}},
			}

			require.NoError(t, verifyAccess(context.Background(), be, project, "synthetic-token", access))
			allowed.Store(false)
			err = verifyAccess(context.Background(), be, project, "synthetic-token", access)
			if tc.wantDenied {
				require.ErrorIs(t, err, ErrPermissionDenied)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tc.wantCalls, calls.Load())
		})
	}
}
