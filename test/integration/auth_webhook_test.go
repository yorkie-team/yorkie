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

	"github.com/yorkie-team/yorkie/api/converter"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/test/helper"
)

var allWebhookMethods = &[]string{
	string(types.ActivateClient),
	string(types.DeactivateClient),
	string(types.AttachDocument),
	string(types.DetachDocument),
	string(types.RemoveDocument),
	string(types.PushPull),
	string(types.WatchDocuments),
	string(types.Broadcast),
}

func newAuthServer(t *testing.T) (*httptest.Server, string) {
	token := xid.New().String()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		if req.Token == token {
			w.WriteHeader(http.StatusOK) // 200
			res.Allowed = true
		} else if req.Token == "not allowed token" {
			w.WriteHeader(http.StatusForbidden) // 403
			res.Allowed = false
		} else if req.Token == "" {
			w.WriteHeader(http.StatusUnauthorized) // 401
			res.Allowed = false
			res.Reason = "no token"
		} else {
			w.WriteHeader(http.StatusUnauthorized) // 401
			res.Allowed = false
			res.Reason = "invalid token"
		}

		_, err = res.Write(w)
		assert.NoError(t, err)
	})), token
}

func newUnavailableAuthServer(t *testing.T, recoveryCnt uint64) *httptest.Server {
	var requestCount uint64
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		res.Allowed = true

		if requestCount < recoveryCnt {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_, err = res.Write(w)
		assert.NoError(t, err)
		requestCount++
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

	t.Run("successful authorization test", func(t *testing.T) {
		ctx := context.Background()
		authServer, token := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
	})

	t.Run("unauthenticated response test", func(t *testing.T) {
		ctx := context.Background()
		authServer, _ := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
			},
		)
		assert.NoError(t, err)

		// client without token
		cliWithoutToken, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliWithoutToken.Close()) }()
		err = cliWithoutToken.Activate(ctx)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		assert.Equal(t, map[string]string{
			"code":   connecthelper.CodeOf(auth.ErrUnauthenticated),
			"reason": "no token",
		}, converter.ErrorMetadataOf(err))

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
		assert.Equal(t, map[string]string{
			"code":   connecthelper.CodeOf(auth.ErrUnauthenticated),
			"reason": "invalid token",
		}, converter.ErrorMetadataOf(err))
	})

	t.Run("permission denied response test", func(t *testing.T) {
		ctx := context.Background()
		authServer, _ := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		_, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
			},
		)
		assert.NoError(t, err)

		// client with not allowed token
		cliNotAllowed, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken("not allowed token"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliNotAllowed.Close()) }()
		err = cliNotAllowed.Activate(ctx)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.Equal(t, connecthelper.CodeOf(auth.ErrPermissionDenied), converter.ErrorCodeOf(err))
	})

	t.Run("selected method authorization webhook test", func(t *testing.T) {
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

		projectCacheTTL := 5 * time.Second
		time.Sleep(projectCacheTTL)
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
		err = cli.Attach(ctx, doc, client.WithRealtimeSync())
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

		_, _, err = cli.Subscribe(doc)
		assert.Equal(t, client.ErrDocumentNotAttached, err)
	})
}

func TestAuthWebhookErrorHandling(t *testing.T) {
	var recoveryCnt uint64 = 4

	conf := helper.TestConfig()
	conf.Backend.AuthWebhookMaxRetries = recoveryCnt
	conf.Backend.AuthWebhookMaxWaitInterval = "1000ms"
	svr, err := server.New(conf)
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	t.Run("unexpected status code test", func(t *testing.T) {
		ctx := context.Background()
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			var res types.AuthWebhookResponse
			res.Allowed = true

			// unexpected status code
			w.WriteHeader(http.StatusBadRequest)

			_, err = res.Write(w)
			assert.NoError(t, err)
		}))

		// project with authorization webhook
		project, err := adminCli.CreateProject(context.Background(), "unexpected-status-code")
		assert.NoError(t, err)

		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken("token"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		err = cli.Activate(ctx)
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
		assert.Equal(t, connecthelper.CodeOf(webhook.ErrUnexpectedStatusCode), converter.ErrorCodeOf(err))
	})

	t.Run("unexpected webhook response test", func(t *testing.T) {
		ctx := context.Background()
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			var res types.AuthWebhookResponse
			// mismatched response
			res.Allowed = false

			_, err = res.Write(w)
			assert.NoError(t, err)
		}))

		// project with authorization webhook
		project, err := adminCli.CreateProject(context.Background(), "unexpected-response-code")
		assert.NoError(t, err)

		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithAPIKey(project.PublicKey),
			client.WithToken("token"),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		err = cli.Activate(ctx)
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
		assert.Equal(t, connecthelper.CodeOf(webhook.ErrUnexpectedResponse), converter.ErrorCodeOf(err))
	})

	t.Run("unavailable authentication server test(timeout)", func(t *testing.T) {
		ctx := context.Background()
		authServer := newUnavailableAuthServer(t, recoveryCnt+1)

		project, err := adminCli.CreateProject(context.Background(), "unavailable-auth-server")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
		assert.Equal(t, connect.CodeInternal, connect.CodeOf(err))
		assert.Equal(t, connecthelper.CodeOf(webhook.ErrWebhookTimeout), converter.ErrorCodeOf(err))
	})

	t.Run("successful authorization after temporarily unavailable server test", func(t *testing.T) {
		ctx := context.Background()
		authServer := newUnavailableAuthServer(t, recoveryCnt)

		project, err := adminCli.CreateProject(context.Background(), "success-webhook-after-retries")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
}

func TestAuthWebhookCache(t *testing.T) {
	t.Run("authorized response cache test", func(t *testing.T) {
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

		authTTL := 1 * time.Second
		conf := helper.TestConfig()
		conf.Backend.AuthWebhookCacheTTL = authTTL.String()

		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "authorized-response-cache")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
		for range 3 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		// 02. multiple requests to update the document after eviction by ttl.
		time.Sleep(authTTL)
		for range 3 {
			assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		assert.Equal(t, 2, reqCnt)
	})

	t.Run("permission denied response cache test", func(t *testing.T) {
		ctx := context.Background()
		reqCnt := 0
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			w.WriteHeader(http.StatusForbidden)
			var res types.AuthWebhookResponse
			res.Allowed = false

			_, err = res.Write(w)
			assert.NoError(t, err)

			reqCnt++
		}))

		authTTL := 1 * time.Second
		conf := helper.TestConfig()
		conf.Backend.AuthWebhookCacheTTL = authTTL.String()

		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "permission-denied-cache")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
		for range 3 {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		}

		// 02. multiple requests after eviction by ttl.
		time.Sleep(authTTL)
		for range 3 {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		}
		assert.Equal(t, 2, reqCnt)
	})

	t.Run("other response not cached test", func(t *testing.T) {
		ctx := context.Background()
		reqCnt := 0
		authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			w.WriteHeader(http.StatusUnauthorized)
			var res types.AuthWebhookResponse
			res.Allowed = false

			_, err = res.Write(w)
			assert.NoError(t, err)

			reqCnt++
		}))

		authTTL := 1 * time.Second
		conf := helper.TestConfig()
		conf.Backend.AuthWebhookCacheTTL = authTTL.String()

		svr, err := server.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "other-response-not-cached")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
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
		for range 3 {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		}

		// 02. multiple requests after eviction by ttl.
		time.Sleep(authTTL)
		for range 3 {
			err = cli.Activate(ctx)
			assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))
		}
		assert.Equal(t, 6, reqCnt)
	})
}

func TestAuthWebhookNewToken(t *testing.T) {
	t.Run("set valid token after invalid token test", func(t *testing.T) {
		ctx := context.Background()
		authServer, validToken := newAuthServer(t)

		svr, err := server.New(helper.TestConfig())
		assert.NoError(t, err)
		assert.NoError(t, svr.Start())
		defer func() { assert.NoError(t, svr.Shutdown(true)) }()

		adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
		defer func() { adminCli.Close() }()
		project, err := adminCli.CreateProject(context.Background(), "new-auth-token")
		assert.NoError(t, err)
		project.AuthWebhookURL = authServer.URL
		_, err = adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				AuthWebhookURL:     &project.AuthWebhookURL,
				AuthWebhookMethods: allWebhookMethods,
			},
		)
		assert.NoError(t, err)

		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken("invalid"),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

		// activate again with valid token
		metadata := converter.ErrorMetadataOf(err)
		assert.Equal(t, "invalid token", metadata["reason"])
		cli.SetToken(validToken)
		assert.NoError(t, cli.Activate(ctx))
	})
}
