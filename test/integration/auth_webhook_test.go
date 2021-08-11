// +build integration

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

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/proxy"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie"
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

func TestAuthWebhook(t *testing.T) {
	t.Run("authorization webhook test", func(t *testing.T) {
		server, token := newAuthServer(t)

		// agent with authorization webhook
		agent, err := yorkie.New(helper.TestConfig(server.URL))
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		// client with token
		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: token})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()
		assert.NoError(t, cli.Activate(ctx))
		defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()

		doc := document.New(helper.Collection, t.Name())
		assert.NoError(t, cli.Attach(ctx, doc))

		// client without token
		cliWithoutToken, err := client.Dial(agent.RPCAddr())
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliWithoutToken.Close()) }()
		err = cliWithoutToken.Activate(ctx)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())

		// client with invalid token
		cliWithInvalidToken, err := client.Dial(agent.RPCAddr(), client.Option{Token: "invalid"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cliWithInvalidToken.Close()) }()
		err = cliWithInvalidToken.Activate(ctx)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
	})

	t.Run("Selected method authorization webhook test", func(t *testing.T) {
		server, _ := newAuthServer(t)

		conf := helper.TestConfig(server.URL)
		conf.Backend.AuthWebhookMethods = []string{
			string(types.AttachDocument),
			string(types.WatchDocuments),
		}

		agent, err := yorkie.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: "invalid"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.Collection, t.Name())
		err = cli.Attach(ctx, doc)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())

		_, err = cli.Watch(ctx, doc)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
	})

	t.Run("authorization webhook that success after retries test", func(t *testing.T) {
		var recoveryCnt uint64
		recoveryCnt = 4
		server := newUnavailableAuthServer(t, recoveryCnt)

		conf := helper.TestConfig(server.URL)
		conf.Backend.AuthWebhookMaxRetries = recoveryCnt
		conf.Backend.AuthWebhookMaxWaitInterval = "1000ms"
		agent, err := yorkie.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: "token"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.Collection, t.Name())
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)
	})

	t.Run("authorization webhook that fails after retries test", func(t *testing.T) {
		server := newUnavailableAuthServer(t, 4)

		conf := helper.TestConfig(server.URL)
		conf.Backend.AuthWebhookMaxRetries = 2
		conf.Backend.AuthWebhookMaxWaitInterval = "1000ms"
		agent, err := yorkie.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: "token"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
	})

	t.Run("authorized request cache test", func(t *testing.T) {
		reqCnt := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		var authorizedTTLSec uint64 = 1
		conf := helper.TestConfig(server.URL)
		conf.Backend.AuthorizationWebhookCacheAuthorizedTTLSec = authorizedTTLSec

		agent, err := yorkie.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: "token"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		err = cli.Activate(ctx)
		assert.NoError(t, err)

		doc := document.New(helper.Collection, t.Name())
		err = cli.Attach(ctx, doc)
		assert.NoError(t, err)

		// 01. multiple requests to update the document.
		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		// 02. multiple requests to update the document after eviction by ttl.
		time.Sleep(time.Duration(authorizedTTLSec) * time.Second)
		for i := 0; i < 3; i++ {
			assert.NoError(t, doc.Update(func(root *proxy.ObjectProxy) error {
				root.SetNewObject("k1")
				return nil
			}))
			assert.NoError(t, cli.Sync(ctx))
		}

		assert.Equal(t, 2, reqCnt)
	})

	t.Run("unauthorized request cache test", func(t *testing.T) {
		reqCnt := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, err := types.NewAuthWebhookRequest(r.Body)
			assert.NoError(t, err)

			var res types.AuthWebhookResponse
			res.Allowed = false

			_, err = res.Write(w)
			assert.NoError(t, err)

			reqCnt++
		}))

		var unauthorizedTTLSec uint64 = 1
		conf := helper.TestConfig(server.URL)
		conf.Backend.AuthorizationWebhookCacheUnauthorizedTTLSec = unauthorizedTTLSec

		agent, err := yorkie.New(conf)
		assert.NoError(t, err)
		assert.NoError(t, agent.Start())
		defer func() { assert.NoError(t, agent.Shutdown(true)) }()

		ctx := context.Background()
		cli, err := client.Dial(agent.RPCAddr(), client.Option{Token: "token"})
		assert.NoError(t, err)
		defer func() { assert.NoError(t, cli.Close()) }()

		// 01. multiple requests.
		for i := 0; i < 3; i++ {
			err = cli.Activate(ctx)
			assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
		}

		// 02. multiple requests after eviction by ttl.
		time.Sleep(time.Duration(unauthorizedTTLSec) * time.Second)
		for i := 0; i < 3; i++ {
			err = cli.Activate(ctx)
			assert.Equal(t, codes.Unauthenticated, status.Convert(err).Code())
		}
		assert.Equal(t, 2, reqCnt)
	})
}
