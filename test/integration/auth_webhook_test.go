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
	"github.com/yorkie-team/yorkie/pkg/key"
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

// TestAuthWebhookPresenceOnly verifies that a webhook can let a read-only
// member attach, detach and send presence while rejecting its edits, by
// allowing a change pack that is presenceOnly.
//
// Attach always carries the initial presence and detach the presence clear,
// so both report verb rw. A webhook that rejects rw for a read-only member
// blocks every attach and detach, and one that allows rw on AttachDocument
// lets a root edit made before the attach ride along with it.
func TestAuthWebhookPresenceOnly(t *testing.T) {
	ctx := context.Background()

	// The attributes are recorded per token, so an assertion about what the
	// reader was asked is not satisfied by the writer being asked the same
	// thing about the same document.
	type call struct {
		token  string
		method types.Method
	}

	var mu sync.Mutex
	seen := map[call][]types.AccessAttribute{}
	authServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, err := types.NewAuthWebhookRequest(r.Body)
		assert.NoError(t, err)

		mu.Lock()
		seen[call{req.Token, req.Method}] = append(seen[call{req.Token, req.Method}], req.Attributes...)
		mu.Unlock()

		// The reader may read and hold presence but must not edit.
		res := types.AuthWebhookResponse{Allowed: true}
		for _, attr := range req.Attributes {
			presenceOnly := attr.PresenceOnly != nil && *attr.PresenceOnly
			if req.Token == "reader" && attr.Verb == types.ReadWrite && !presenceOnly {
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
	project, err := adminCli.CreateProject(ctx, "presence-only")
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

	yes, no := true, false

	// createDoc has the writer bring the document into being, which a reader
	// may not do: creating the document is a write the pack does not show,
	// and the attach that creates it is asked for that write.
	createDoc := func(t *testing.T, docKey key.Key, opts ...any) {
		doc := document.New(docKey)
		assert.NoError(t, writer.Attach(ctx, doc, opts...))
		assert.NoError(t, writer.Detach(ctx, doc))
	}

	t.Run("a reader cannot create the document", func(t *testing.T) {
		docKey := helper.TestKey(t)

		// The pack of this attach is presence only, so the first check allows
		// it; the attach would create the document, so it is asked again for
		// a document write and rejected.
		err := reader.Attach(ctx, document.New(docKey))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		mu.Lock()
		asked := seen[call{"reader", types.AttachDocument}]
		mu.Unlock()
		assert.Contains(t, asked, types.AccessAttribute{
			Key: docKey.String(), Verb: types.ReadWrite, PresenceOnly: &yes,
		})
		// The second question carries no presenceOnly at all, so a webhook
		// that treats a missing field as "not presence only" rejects it.
		assert.Contains(t, asked, types.AccessAttribute{
			Key: docKey.String(), Verb: types.ReadWrite, PresenceOnly: nil,
		})

		// Nothing was created: the attach is rejected again rather than
		// finding the document its first attempt would have inserted. Once
		// the writer creates it, the same attach passes.
		err = reader.Attach(ctx, document.New(docKey))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		createDoc(t, docKey)
		doc := document.New(docKey)
		assert.NoError(t, reader.Attach(ctx, doc))
		assert.NoError(t, reader.Detach(ctx, doc))
	})

	t.Run("a reader attaches, sends presence and detaches", func(t *testing.T) {
		docKey := helper.TestKey(t)
		createDoc(t, docKey)

		doc := document.New(docKey)
		assert.NoError(t, reader.Attach(ctx, doc))
		assert.NoError(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			p.Set("cursor", "1")
			return nil
		}))
		assert.NoError(t, reader.Sync(ctx))
		assert.NoError(t, reader.Detach(ctx, doc))

		// The verb is unchanged: presence is still reported as a write.
		mu.Lock()
		defer mu.Unlock()
		want := types.AccessAttribute{Key: doc.Key().String(), Verb: types.ReadWrite, PresenceOnly: &yes}
		for _, m := range []types.Method{types.AttachDocument, types.PushPull, types.DetachDocument} {
			assert.Contains(t, seen[call{"reader", m}], want, m)
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
		docKey := helper.TestKey(t)
		createDoc(t, docKey)

		doc := document.New(docKey)
		assert.NoError(t, reader.Attach(ctx, doc))
		err := reader.Remove(ctx, doc)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		assert.NoError(t, reader.Detach(ctx, doc))

		mu.Lock()
		defer mu.Unlock()
		assert.Contains(t, seen[call{"reader", types.RemoveDocument}], types.AccessAttribute{
			Key: doc.Key().String(), Verb: types.ReadWrite, PresenceOnly: &no,
		})
	})

	t.Run("a reader cannot bind a schema but can attach under one", func(t *testing.T) {
		const schemaKey = "note@1"
		noteRules := []types.Rule{{Path: "$.title", Type: "string"}}
		assert.NoError(t, adminCli.CreateSchema(
			ctx,
			project.Name,
			"note",
			1,
			"type Document = {title: string;};",
			noteRules,
		))

		// An attach that finds no client attached and names a schema the
		// document is not bound to binds it, whether its pack is presence
		// only or, for a presence-disabled document, empty. The documents
		// exist, so what rejects these attaches is the binding, not the
		// create.
		unbound, disabled := helper.TestKey(t, 1), helper.TestKey(t, 3)
		createDoc(t, unbound)
		createDoc(t, disabled, client.WithDisablePresence())

		err := reader.Attach(ctx, document.New(unbound), client.WithSchema(schemaKey))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))
		err = reader.Attach(
			ctx,
			document.New(disabled),
			client.WithSchema(schemaKey),
			client.WithDisablePresence(),
		)
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		// The rejection bound nothing: the document still has no ruleset.
		after := document.New(unbound)
		assert.NoError(t, writer.Attach(ctx, after))
		assert.Equal(t, []types.Rule(nil), after.SchemaRules)
		assert.NoError(t, writer.Detach(ctx, after))

		// Once a writer has bound it, attaching under the schema binds
		// nothing and the reader is handed the binding that is already there.
		boundKey := helper.TestKey(t, 2)
		bound := document.New(boundKey)
		assert.NoError(t, writer.Attach(ctx, bound, client.WithSchema(schemaKey)))
		doc := document.New(boundKey)
		assert.NoError(t, reader.Attach(ctx, doc, client.WithSchema(schemaKey)))
		assert.Equal(t, noteRules, doc.SchemaRules)
		assert.NoError(t, reader.Detach(ctx, doc))
		assert.NoError(t, writer.Detach(ctx, bound))
	})

	t.Run("a reader attaching alone keeps the bound schema", func(t *testing.T) {
		// With an attachment limit, every attach that finds no one attached
		// reaches the rebind; a reader naming the bound schema or none must
		// pass without being asked for a write, and one naming another
		// schema must not.
		limit := 10
		_, err := adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
			MaxAttachmentsPerDocument: &limit,
		})
		assert.NoError(t, err)
		defer func() {
			off := 0
			_, err := adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
				MaxAttachmentsPerDocument: &off,
			})
			assert.NoError(t, err)
		}()
		memoRules := []types.Rule{{Path: "$.title", Type: "string"}}
		assert.NoError(t, adminCli.CreateSchema(
			ctx,
			project.Name,
			"memo",
			1,
			"type Document = {title: string;};",
			memoRules,
		))

		docKey := helper.TestKey(t)
		bound := document.New(docKey)
		assert.NoError(t, writer.Attach(ctx, bound, client.WithSchema("memo@1")))
		assert.NoError(t, writer.Detach(ctx, bound))

		// Naming the bound schema keeps it: the server hands back its
		// ruleset, so the attach neither rebound nor unbound the document.
		doc := document.New(docKey)
		assert.NoError(t, reader.Attach(ctx, doc, client.WithSchema("memo@1")))
		assert.Equal(t, memoRules, doc.SchemaRules)
		assert.NoError(t, reader.Detach(ctx, doc))

		// Naming none keeps it too, and the ruleset is still enforced: a
		// write that violates it is rejected.
		doc = document.New(docKey)
		assert.NoError(t, reader.Attach(ctx, doc))
		assert.Equal(t, memoRules, doc.SchemaRules)
		assert.ErrorIs(t, doc.Update(func(r *json.Object, p *presence.Presence) error {
			r.SetInteger("title", 1)
			return nil
		}), document.ErrSchemaValidationFailed)
		assert.NoError(t, reader.Detach(ctx, doc))

		err = reader.Attach(ctx, document.New(docKey), client.WithSchema("note@1"))
		assert.Equal(t, connect.CodePermissionDenied, connect.CodeOf(err))

		// The rejected rebind left the binding alone.
		after := document.New(docKey)
		assert.NoError(t, writer.Attach(ctx, after))
		assert.Equal(t, memoRules, after.SchemaRules)
		assert.NoError(t, writer.Detach(ctx, after))
	})

	t.Run("a writer's edit is not presenceOnly", func(t *testing.T) {
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
		assert.Contains(t, seen[call{"writer", types.PushPull}], types.AccessAttribute{
			Key: doc.Key().String(), Verb: types.ReadWrite, PresenceOnly: &no,
		})
	})
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
