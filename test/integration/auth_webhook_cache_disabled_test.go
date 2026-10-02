//go:build integration

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

package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func TestAuthWebhookCacheDisabledOnServer(t *testing.T) {
	ctx := context.Background()
	var allowed atomic.Bool
	allowed.Store(true)
	var pushPullCalls atomic.Int64
	var activateCalls atomic.Int64
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The handler runs off the test goroutine, where require's FailNow
		// is not allowed; report with assert and fail the request instead.
		req, err := types.NewAuthWebhookRequest(r.Body)
		if !assert.NoError(t, err) {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch req.Method {
		case types.PushPull:
			pushPullCalls.Add(1)
		case types.ActivateClient:
			activateCalls.Add(1)
		}
		if !allowed.Load() {
			w.WriteHeader(http.StatusForbidden)
		}
		_, err = (&types.AuthWebhookResponse{Allowed: allowed.Load()}).Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	conf := helper.TestConfig()
	conf.Mongo = nil // The authorization check needs no external database.
	conf.Backend.AuthWebhookCacheDisabled = true
	svr, err := server.New(conf)
	require.NoError(t, err)
	require.NoError(t, svr.Start())
	defer func() { require.NoError(t, svr.Shutdown(true)) }()

	admin := helper.CreateAdminCli(t, svr.RPCAddr())
	defer admin.Close()
	project, err := admin.CreateProject(ctx, "disabled-auth-cache")
	require.NoError(t, err)
	methods := []string{string(types.ActivateClient), string(types.AttachDocument), string(types.PushPull)}
	_, err = admin.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		AuthWebhookURL:     &authServer.URL,
		AuthWebhookMethods: &methods,
	})
	require.NoError(t, err)

	cli, err := client.Dial(svr.RPCAddr(), client.WithAPIKey(project.PublicKey), client.WithToken("synthetic-token"))
	require.NoError(t, err)
	defer func() { require.NoError(t, cli.Close()) }()
	require.NoError(t, cli.Activate(ctx))
	doc := document.New(helper.TestKey(t))
	require.NoError(t, cli.Attach(ctx, doc))
	require.NoError(t, cli.Sync(ctx))
	beforeRevocation := pushPullCalls.Load()
	require.Positive(t, beforeRevocation)

	allowed.Store(false)
	err = cli.Sync(ctx)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Equal(t, beforeRevocation+1, pushPullCalls.Load())

	second, err := client.Dial(svr.RPCAddr(), client.WithAPIKey(project.PublicKey), client.WithToken("synthetic-token"))
	require.NoError(t, err)
	defer func() { require.NoError(t, second.Close()) }()
	err = second.Activate(ctx)
	require.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
	require.Equal(t, int64(2), activateCalls.Load())
}
