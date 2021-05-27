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
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/xid"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/types"
	"github.com/yorkie-team/yorkie/test/helper"
	"github.com/yorkie-team/yorkie/yorkie"
)

func newAuthServer(t *testing.T) (*httptest.Server, string) {
	token := xid.New().String()

	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req types.AuthWebhookRequest
		assert.NoError(t, json.NewDecoder(r.Body).Decode(&req))

		var res types.AuthWebhookResponse
		if req.Token == token {
			res.Allowed = true
		} else {
			res.Reason = "invalid token"
		}
		bytes, err := json.Marshal(res)
		_, err = w.Write(bytes)
		assert.NoError(t, err)
	})), token
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
}
