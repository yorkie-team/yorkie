//go:build integration

/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func newAuthServer(t *testing.T) (*httptest.Server, string) {
	token := xid.New().String()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		if req.Token == token {
			res.Allowed = true
		} else {
			res.Reason = "invalid token"
		}

		_, err = res.Write(w)
		assert.NoError(t, err)
	})), token
}

func newUnavailableAuthServer(t *testing.T, recoveryCnt uint64) *httptest.Server {
	var retries uint64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		res.Allowed = true
		if retries < recoveryCnt-1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			retries++
		} else {
			retries = 0
		}

		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
}

func TestProjectAuthWebhook(t *testing.T) {
	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	project, err := adminCli.CreateProject(context.Background(), "auth-webhook-test")
	assert.NoError(t, err)

	t.Run("authorization webhook test", func(t *testing.T) {
		ctx := context.Background()
		authServer, token := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL: &project.AuthWebhookURL,
			},
		)
		assert.NoError(t, err)

		// client with token
		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken(token),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))
		defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()

		doc := document.New(helper.TestDocKey(t))
		assert.NoError(t, cli.Attach(ctx, doc))

		// client without token
		cliWithoutToken, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliWithoutToken.Close()) }()
		err = cliWithoutToken.Activate(ctx)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

		// client with invalid token
		cliWithInvalidToken, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken("invalid"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliWithInvalidToken.Close()) }()
		err = cliWithInvalidToken.Activate(ctx)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("Selected method authorization webhook test", func(t *testing.T) {
		ctx := context.Background()
		authServer, _ := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		project.AuthWebhookMethods = []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}
		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: &project.AuthWebhookMethods,
			},
		)
		assert.NoError(t, err)

		projectInfoCacheTTL := 5 * time.Second
		time.Sleep(projectInfoCacheTTL)
		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken("invalid"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

		_, _, err = cli.Subscribe(doc)
		assert.Equal(t, client.ErrDocumentNotAttached, err)
	})
}

func TestAuthWebhook(t *testing.T) {
	t.Run("authorization webhook that success after retries test", func(t *testing.T) {
		ctx := context.Background()

		var recoveryCnt uint64
		recoveryCnt = 4
		authServer := newUnavailableAuthServer(t, recoveryCnt)

		conf := helper.TestConfig()
		conf.Backend.AuthWebhookMaxRetries = recoveryCnt
		conf.Backend.AuthWebhookMaxWaitInterval = "1000ms"
		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "success-webhook-after-retries")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL: &project.AuthWebhookURL,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken("token"),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)
	})

	t.Run("authorization webhook that fails after retries test", func(t *testing.T) {
		ctx := context.Background()
		authServer := newUnavailableAuthServer(t, 4)

		conf := helper.TestConfig()
		conf.Backend.AuthWebhookMaxRetries = 2
		conf.Backend.AuthWebhookMaxWaitInterval = "1000ms"
		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "fail-webhook-after-retries")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL: &project.AuthWebhookURL,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken("token"),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
	})

	t.Run("authorized request cache test", func(t *testing.T) {
		ctx := context.Background()
		reqCnt := 0
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			req, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			var res types.AuthWebhookResponse
			res.Allowed = true

			_, err = res.Write(w)
			assert.NoError(t, err)

			if req.Method == types.PushPull {
				reqCnt++
			}
		}))

		authorizedTTL := 1 * time.Second
		conf := helper.TestConfig()
		conf.Backend.AuthWebhookCacheAuthTTL = authorizedTTL.String()

		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "auth-request-cache")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL: &project.AuthWebhookURL,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken("token"),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.TestDocKey(t))
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)

		// 01. multiple requests to update the document.
		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		// 02. multiple requests to update the document after eviction by ttl.
		time.Sleep(authorizedTTL)
		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		assert.Equal(t, 2, reqCnt)
	})

	t.Run("unauthorized request cache test", func(t *testing.T) {
		ctx := context.Background()
		reqCnt := 0
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			var res types.AuthWebhookResponse
			res.Allowed = false

			_, err = res.Write(w)
			assert.NoError(t, err)

			reqCnt++
		}))

		unauthorizedTTL := 1 * time.Second
		conf := helper.TestConfig()
		conf.Backend.AuthWebhookCacheUnauthTTL = unauthorizedTTL.String()

		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "unauth-request-cache")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL: &project.AuthWebhookURL,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken("token"),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		// 01. multiple requests.
		for i := 0; i < 3; i++ {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		}

		// 02. multiple requests after eviction by ttl.
		time.Sleep(unauthorizedTTL)
		for i := 0; i < 3; i++ {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		}
		assert.Equal(t, 2, reqCnt)
	})
}
