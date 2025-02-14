/*
 * Copyright 2025 The Yorkie Authors. All rights reserved.
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
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/atomic"

	"github.com/yorkie-team/yorkie/pkg/document/crdt"

	docjson "github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/server"
	"github.com/yorkie-team/yorkie/test/helper"
)

func verifySignature(signatureHeader, secret string, body []byte) error {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedSig := hex.EncodeToString(mac.Sum(nil))
	expectedSigHeader := fmt.Sprintf("sha256=%s", expectedSig)
	if !hmac.Equal([]byte(signatureHeader), []byte(expectedSigHeader)) {
		return errors.New("signature validation failed")
	}
	return nil
}

func newUserServer(t *testing.T, requestCounter *atomic.Int32, secretKey, docKey string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCounter.Add(1)
		signatureHeader := r.Header.Get("X-Signature-256")
		if signatureHeader == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		if err := verifySignature(signatureHeader, secretKey, body); err != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		req := &types.EventWebhookRequest{}
		assert.NoError(t, json.Unmarshal(body, req))
		assert.Equal(t, types.DocRootChanged, req.Type)
		assert.Equal(t, docKey, req.Attributes.Key)

		assert.NoError(t, err)
		w.WriteHeader(http.StatusOK)
	}))
}

func TestRegisterEventWebhook(t *testing.T) {
	// 01. Initialize server
	conf := helper.TestConfig()
	conf.Backend.EventWebhookCacheTTL = "0ms"
	conf.Backend.ProjectCacheTTL = "0ms"
	svr, err := server.New(conf)
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	defer func() { assert.NoError(t, svr.Shutdown(true)) }()

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	project, err := adminCli.CreateProject(context.Background(), "event-webhook-test")
	assert.NoError(t, err)

	ctx := context.Background()
	docKey := helper.TestDocKey(t)
	doc := document.New(docKey)
	requestCnt := atomic.NewInt32(0)
	userServer := newUserServer(t, requestCnt, project.SecretKey, docKey.String())

	cli, err := client.Dial(
		svr.RPCAddr(),
		client.WithAPIKey(project.PublicKey),
	)
	assert.NoError(t, err)
	defer func() { assert.NoError(t, cli.Close()) }()
	assert.NoError(t, cli.Activate(ctx))
	defer func() { assert.NoError(t, cli.Deactivate(ctx)) }()
	assert.NoError(t, cli.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
		"counter": docjson.NewCounter(0, crdt.LongCnt),
	})))

	t.Run("register and unregister event webhook test", func(t *testing.T) {
		project.EventWebhookURL = userServer.URL
		prj, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				EventWebhookURL:   &project.EventWebhookURL,
				EventWebhookTypes: &[]string{string(types.DocRootChanged)},
			},
		)
		assert.NoError(t, err)
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, string(types.DocRootChanged), prj.EventWebhookTypes[0])

		prev := requestCnt.Load()
		assert.NoError(t, doc.Update(func(root *docjson.Object, p *presence.Presence) error {
			cnt := root.GetCounter("counter")
			cnt.Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, prev+1, requestCnt.Load())
	})

	t.Run("register and unregister event webhook test", func(t *testing.T) {
		prj, err := adminCli.UpdateProject(
			ctx,
			project.ID.String(),
			&types.UpdatableProjectFields{
				EventWebhookURL:   &project.EventWebhookURL,
				EventWebhookTypes: &[]string{},
			},
		)
		assert.NoError(t, err)
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, 0, len(prj.EventWebhookTypes))

		prev := requestCnt.Load()
		assert.NoError(t, doc.Update(func(root *docjson.Object, p *presence.Presence) error {
			cnt := root.GetCounter("counter")
			cnt.Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, prev, requestCnt.Load())
	})
}

func TestProjectEventWebhookCache(t *testing.T) {
	t.Run("DocRootChanged event test", func(t *testing.T) {

	})

	t.Run("throttling test", func(t *testing.T) {

	})
}
