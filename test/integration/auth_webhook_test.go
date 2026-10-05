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
	"sync"
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
	"github.com/yorkie-team/yorkie/test/helper"
)

var allWebhookMethods = &[]string{
	string(types.ActivateClient),
	string(types.DeactivateClient),
	string(types.AttachDocument),
	string(types.DetachDocument),
	string(types.RemoveDocument),
	string(types.PushPull),
	string(types.Watch),
	string(types.Broadcast),
}

func newAuthServer(t *testing.T) (*httptest.Server, string) {
	token := xid.New().String()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		switch req.Token {
		case token:
			w.WriteHeader(http.StatusOK) // 200
			res.Allowed = true
		case "not allowed token":
			w.WriteHeader(http.StatusForbidden) // 403
			res.Allowed = false
		case "":
			w.WriteHeader(http.StatusUnauthorized) // 401
			res.Allowed = false
			res.Reason = "no token"
		default:
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

		doc := document.New(helper.TestKey(t))
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
		assert.Equal(t, map[string]string{"code": auth.ErrUnauthenticated.Code(), "reason": "no token"}, converter.ErrorMetadataOf(err))

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
		assert.Equal(t, map[string]string{"code": auth.ErrUnauthenticated.Code(), "reason": "invalid token"}, converter.ErrorMetadataOf(err))
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
		assert.Equal(t, auth.ErrPermissionDenied.Code(), converter.ErrorCodeOf(err))
	})

	t.Run("selected method authorization webhook test", func(t *testing.T) {
		ctx := context.Background()
		authServer, _ := newAuthServer(t)

		// project with authorization webhook
		project.AuthWebhookURL = authServer.URL
		project.AuthWebhookMethods = []string{
			string(types.AttachDocument),
			string(types.Watch),
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

		doc := document.New(helper.TestKey(t))
		err = cli.Attach(ctx, doc, client.WithRealtimeSync())
		assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

		_, _, err = cli.WatchStream(doc)
		assert.Equal(t, client.ErrNotAttached, err)
	})
}

func TestAuthWebhookErrorHandling(t *testing.T) {
	var recoveryCnt uint64 = 4

	conf := helper.TestConfig()
	svr, err := server.New(conf)
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	authWebhookMaxRetries := recoveryCnt
	authWebhookMaxWaitInterval := "1000ms"
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
				AuthWebhookURL:             &project.AuthWebhookURL,
				AuthWebhookMethods:         allWebhookMethods,
				AuthWebhookMaxRetries:      &authWebhookMaxRetries,
				AuthWebhookMaxWaitInterval: &authWebhookMaxWaitInterval,
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
		assert.Equal(t, webhook.ErrUnexpectedStatusCode.Code(), converter.ErrorCodeOf(err))
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
				AuthWebhookURL:             &project.AuthWebhookURL,
				AuthWebhookMethods:         allWebhookMethods,
				AuthWebhookMaxRetries:      &authWebhookMaxRetries,
				AuthWebhookMaxWaitInterval: &authWebhookMaxWaitInterval,
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
		assert.Equal(t, webhook.ErrInvalidJSONResponse.Code(), converter.ErrorCodeOf(err))
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
				AuthWebhookURL:             &project.AuthWebhookURL,
				AuthWebhookMethods:         allWebhookMethods,
				AuthWebhookMaxRetries:      &authWebhookMaxRetries,
				AuthWebhookMaxWaitInterval: &authWebhookMaxWaitInterval,
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
		assert.Equal(t, webhook.ErrWebhookTimeout.Code(), converter.ErrorCodeOf(err))
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
				AuthWebhookURL:             &project.AuthWebhookURL,
				AuthWebhookMethods:         allWebhookMethods,
				AuthWebhookMaxRetries:      &authWebhookMaxRetries,
				AuthWebhookMaxWaitInterval: &authWebhookMaxWaitInterval,
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

		doc := document.New(helper.TestKey(t))
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

		doc := document.New(helper.TestKey(t))
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

// TestAuthWebhookWatchAttributes verifies that Watch asks the auth webhook
// about the resource it would deliver.
//
// Without attributes the webhook is asked only "may this client watch?", with
// no key to decide on, so a deployment authorizing per document cannot deny a
// client that names a document it never attached — and a Watch stream hands
// back that document's peer list, presence and broadcast payloads.
func TestAuthWebhookWatchAttributes(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	var watchAttrs []types.AccessAttribute
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		if req.Method == types.Watch {
			mu.Lock()
			watchAttrs = req.Attributes
			mu.Unlock()
		}

		var res types.AuthWebhookResponse
		res.Allowed = true
		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()
	project, err := adminCli.CreateProject(ctx, "watch-attributes")
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
	assert.NoError(t, cli.Activate(ctx))

	doc := document.New(helper.TestKey(t))
	assert.NoError(t, cli.Attach(ctx, doc, client.WithRealtimeSync()))

	assert.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(watchAttrs) > 0
	}, 5*time.Second, 50*time.Millisecond, "the Watch webhook was never called")

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []types.AccessAttribute{{
		Key:  doc.Key().String(),
		Verb: types.Read,
	}}, watchAttrs)
}

// TestAuthWebhookPresenceOnlyRead verifies that a webhook rejecting rw from a
// read-only member lets it attach and detach, and still stops its writes.
//
// Attach always carries the initial presence and detach the presence clear.
// While a presence-only pack reported rw, such a webhook blocked every attach
// and detach, and one that allowed rw on AttachDocument let a root edit made
// before the attach ride along with it.
func TestAuthWebhookPresenceOnlyRead(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	seen := map[types.Method][]types.AccessAttribute{}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		mu.Lock()
		seen[req.Method] = append(seen[req.Method], req.Attributes...)
		mu.Unlock()

		// The reader may read the document but must not write it.
		res := types.AuthWebhookResponse{Allowed: true}
		for _, attr := range req.Attributes {
			if req.Token == "reader" && attr.Verb == types.ReadWrite {
				res = types.AuthWebhookResponse{Allowed: false, Reason: "read-only member"}
				w.WriteHeader(http.StatusForbidden)
				break
			}
		}
		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()
	project, err := adminCli.CreateProject(ctx, "presence-only-read")
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

	dial := func(token string) *client.Client {
		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken(token),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		return cli
	}
	writer, reader := dial("writer"), dial("reader")
	defer func() { assert.NoError(t, writer.Close()) }()
	defer func() { assert.NoError(t, reader.Close()) }()

	t.Run("a reader attaches and detaches with presence only", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, reader.Attach(ctx, doc))
		assert.NoError(t, reader.Detach(ctx, doc))

		mu.Lock()
		defer mu.Unlock()
		for _, m := range []types.Method{types.AttachDocument, types.DetachDocument} {
			assert.Contains(t, seen[m], types.AccessAttribute{Key: doc.Key().String(), Verb: types.Read})
		}
	})

	t.Run("a reader's edit before attach is rejected with the attach", func(t *testing.T) {
		docKey := helper.TestKey(t)
		doc := document.New(docKey)
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("x", 1)
			return nil
		}))
		err := reader.Attach(ctx, doc)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		other := document.New(docKey)
		assert.NoError(t, writer.Attach(ctx, other))
		assert.Equal(t, `{}`, other.Marshal())
		assert.NoError(t, writer.Detach(ctx, other))
	})

	t.Run("a reader cannot remove the document", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, reader.Attach(ctx, doc))
		err := reader.Remove(ctx, doc)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.NoError(t, reader.Detach(ctx, doc))
	})

	t.Run("a reader cannot bind a schema but can attach under one", func(t *testing.T) {
		const schemaKey = "note@1"
		assert.NoError(t, adminCli.CreateSchema(
			ctx,
			project.Name,
			"note",
			1,
			"type Document = {title: string;};",
			[]types.Rule{{Path: "$.title", Type: "string"}},
		))

		// The first attach to a document binds the schema it names.
		err := reader.Attach(ctx, document.New(helper.TestKey(t, 1)), client.WithSchema(schemaKey))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		// Once a writer has bound it, attaching under the schema is a read.
		bound := document.New(helper.TestKey(t, 2))
		assert.NoError(t, writer.Attach(ctx, bound, client.WithSchema(schemaKey)))
		doc := document.New(helper.TestKey(t, 2))
		assert.NoError(t, reader.Attach(ctx, doc, client.WithSchema(schemaKey)))
		assert.NoError(t, reader.Detach(ctx, doc))
		assert.NoError(t, writer.Detach(ctx, bound))
	})

	// Presence is stored as a change and published to every watcher, so only
	// attach and detach report it as a read. A reader that changes presence
	// mid-attachment goes through PushPull, which reports rw.
	t.Run("a reader's presence update is rejected mid-attachment", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, reader.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			p.Set("cursor", "1")
			return nil
		}))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(reader.Sync(ctx)))

		// The rejected change stays unacknowledged in the client, and a detach
		// pack carries every unacknowledged change, so the detach that follows
		// carries the rejected presence along with its clear. A pack that
		// carries more than the presence the SDK sends on its own is reported
		// as a write for that reason, so the webhook stops the deferred write
		// too. The reader still leaves by deactivating, where the server
		// builds the presence clear itself and the rejected change is dropped.
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(reader.Detach(ctx, doc)))

		mu.Lock()
		defer mu.Unlock()
		for _, m := range []types.Method{types.PushPull, types.DetachDocument} {
			assert.Contains(t, seen[m], types.AccessAttribute{
				Key:  doc.Key().String(),
				Verb: types.ReadWrite,
			})
		}
	})

	t.Run("a writer's edit is sent as rw", func(t *testing.T) {
		doc := document.New(helper.TestKey(t))
		assert.NoError(t, writer.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("x", 1)
			return nil
		}))
		assert.NoError(t, writer.Sync(ctx))
		assert.NoError(t, writer.Detach(ctx, doc))

		mu.Lock()
		defer mu.Unlock()
		assert.Contains(t, seen[types.PushPull], types.AccessAttribute{
			Key:  doc.Key().String(),
			Verb: types.ReadWrite,
		})
	})
}

// TestAuthWebhookRemoveOnDetachRead verifies that under the project's
// RemoveOnDetach, a detach verified as a read does not remove the document
// when the webhook refuses the write, and still lets the member detach.
//
// The server sets pack.IsRemoved itself, after the pack's own verb was
// verified, so without the second check the removal would ride along with a
// presence-only detach reported as r.
func TestAuthWebhookRemoveOnDetachRead(t *testing.T) {
	ctx := context.Background()

	var mu sync.Mutex
	seen := map[types.Method][]types.AccessAttribute{}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		mu.Lock()
		seen[req.Method] = append(seen[req.Method], req.Attributes...)
		mu.Unlock()

		// The reader may read the document but must not write it.
		res := types.AuthWebhookResponse{Allowed: true}
		for _, attr := range req.Attributes {
			if req.Token == "reader" && attr.Verb == types.ReadWrite {
				res = types.AuthWebhookResponse{Allowed: false, Reason: "read-only member"}
				w.WriteHeader(http.StatusForbidden)
				break
			}
		}
		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()
	project, err := adminCli.CreateProject(ctx, "remove-on-detach-read")
	assert.NoError(t, err)
	project.AuthWebhookURL = authServer.URL
	removeOnDetach := true
	_, err = adminCli.UpdateProject(
		ctx,
		project.ID.String(),
		&types.UpdatableProjectFields{
			AuthWebhookURL:     &project.AuthWebhookURL,
			AuthWebhookMethods: allWebhookMethods,
			RemoveOnDetach:     &removeOnDetach,
		},
	)
	assert.NoError(t, err)

	dial := func(token string) *client.Client {
		cli, err := client.Dial(
			svr.RPCAddr(),
			client.WithToken(token),
			client.WithAPIKey(project.PublicKey),
		)
		assert.NoError(t, err)
		assert.NoError(t, cli.Activate(ctx))
		return cli
	}
	writer, reader := dial("writer"), dial("reader")
	defer func() { assert.NoError(t, writer.Close()) }()
	defer func() { assert.NoError(t, reader.Close()) }()

	docKey := helper.TestKey(t)

	// A writer fills the document, so a later attach shows whether it survived.
	seeded := document.New(docKey)
	assert.NoError(t, writer.Attach(ctx, seeded))
	assert.NoError(t, seeded.Update(func(r *json.Object, p *presence.Presence) error {
		r.SetInteger("x", 1)
		return nil
	}))
	assert.NoError(t, writer.Sync(ctx))

	// The reader detaches last, so RemoveOnDetach would remove the document.
	readerDoc := document.New(docKey)
	assert.NoError(t, reader.Attach(ctx, readerDoc))
	assert.NoError(t, writer.Detach(ctx, seeded))
	assert.NoError(t, reader.Detach(ctx, readerDoc))

	mu.Lock()
	assert.Contains(t, seen[types.DetachDocument], types.AccessAttribute{
		Key:  docKey.String(),
		Verb: types.ReadWrite,
	}, "the removal on detach is asked for as a write")
	mu.Unlock()

	// The refused write stopped the removal, not the detach: the document and
	// its content are still there.
	survived := document.New(docKey)
	assert.NoError(t, writer.Attach(ctx, survived))
	assert.Equal(t, `{"x":1}`, survived.Marshal())

	// A writer's detach is allowed to remove it, so the next attach is empty.
	assert.NoError(t, writer.Detach(ctx, survived))
	fresh := document.New(docKey)
	assert.NoError(t, writer.Attach(ctx, fresh))
	assert.Equal(t, `{}`, fresh.Marshal())
	assert.NoError(t, writer.Detach(ctx, fresh))
}

func TestAuthWebhookInitialWatchDenied(t *testing.T) {
	ctx := context.Background()

	// The webhook rejects only the first Watch, and with 401 rather than 403
	// so the server does not cache the denial for the retry below.
	var mu sync.Mutex
	watchCalls := 0
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		var res types.AuthWebhookResponse
		res.Allowed = true
		if req.Method == types.Watch {
			mu.Lock()
			watchCalls++
			first := watchCalls == 1
			mu.Unlock()
			if first {
				w.WriteHeader(http.StatusUnauthorized)
				res.Allowed = false
				res.Reason = "first watch denied"
			}
		}
		_, err = res.Write(w)
		assert.NoError(t, err)
	}))
	defer authServer.Close()

	svr, err := server.New(helper.TestConfig())
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()
	project, err := adminCli.CreateProject(ctx, "initial-watch-denied")
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
	assert.NoError(t, cli.Activate(ctx))
	defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()

	// 01. AttachDocument succeeds on the server, then the initial Watch is
	// denied, so Attach reports the failure.
	doc := document.New(helper.TestKey(t))
	err = cli.Attach(ctx, doc, client.WithRealtimeSync())
	assert.Equal(t, connect.CodeUnauthenticated, connect.CodeOf(err))

	// 02. The server still holds the attachment, so the client keeps it too:
	// the caller recovers by detaching and attaching again.
	assert.NoError(t, cli.Detach(ctx, doc))
	assert.Equal(t, document.StatusDetached, doc.Status())

	doc2 := document.New(helper.TestKey(t))
	assert.NoError(t, cli.Attach(ctx, doc2, client.WithRealtimeSync()))
	_, _, err = cli.WatchStream(doc2)
	assert.NoError(t, err)
	assert.NoError(t, cli.Detach(ctx, doc2))
}
