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
	gojson "encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.uber.org/atomic"

	"github.com/stretchr/testify/assert"
	"github.com/yorkie-team/yorkie/admin"
	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/client"
	"github.com/yorkie-team/yorkie/pkg/document"
	"github.com/yorkie-team/yorkie/pkg/document/crdt"
	"github.com/yorkie-team/yorkie/pkg/document/json"
	"github.com/yorkie-team/yorkie/pkg/document/presence"
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
		assert.NoError(t, gojson.Unmarshal(body, req))
		assert.Equal(t, types.DocRootChanged, req.Type)
		assert.Equal(t, docKey, req.Attributes.Key)

		w.WriteHeader(http.StatusOK)
	}))
}

// setupYorkieServer initializes the Yorkie server and admin client.
func setupYorkieServer(t *testing.T, webhookCacheTTL string) (*server.Yorkie, *admin.Client) {
	conf := helper.TestConfig()
	conf.Backend.EventWebhookCacheTTL = webhookCacheTTL
	conf.Backend.ProjectCacheTTL = "0ms"
	svr, err := server.New(conf)
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())
	t.Cleanup(func() { assert.NoError(t, svr.Shutdown(true)) })

	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	t.Cleanup(func() { adminCli.Close() })

	return svr, adminCli
}

// createProjectAndClient creates a project and sets up a Yorkie client.
func createProjectAndClient(t *testing.T, ctx context.Context, adminCli *admin.Client, rpcAddr string) (*types.Project, *client.Client) {
	project, err := adminCli.CreateProject(ctx, "event-webhook-test")
	assert.NoError(t, err)

	cli, err := client.Dial(rpcAddr, client.WithAPIKey(project.PublicKey))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(ctx))
	t.Cleanup(func() {
		assert.NoError(t, cli.Deactivate(ctx))
		assert.NoError(t, cli.Close())
	})

	return project, cli
}

// attachTestDocument attaches a new document with an initial root.
func attachTestDocument(t *testing.T, ctx context.Context, cli *client.Client) (*document.Document, string) {
	docKey := helper.TestDocKey(t)
	doc := document.New(docKey)
	initialRoot := map[string]any{
		"counter": json.NewCounter(0, crdt.LongCnt),
	}
	assert.NoError(t, cli.Attach(ctx, doc, client.WithInitialRoot(initialRoot)))
	return doc, docKey.String()
}

// registerWebhook updates the project to register (or unregister) the webhook.
func registerWebhook(t *testing.T, ctx context.Context, adminCli *admin.Client, project *types.Project, webhookURL string, eventTypes []string) *types.Project {
	project.EventWebhookURL = webhookURL
	prj, err := adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		EventWebhookURL:   &project.EventWebhookURL,
		EventWebhookTypes: &eventTypes,
	})
	assert.NoError(t, err)
	return prj
}

func TestRegisterEventWebhook(t *testing.T) {
	ctx := context.Background()
	svr, adminCli := setupYorkieServer(t, "0ms")
	project, cli := createProjectAndClient(t, ctx, adminCli, svr.RPCAddr())

	// 01. Attach a new document.
	doc, docKey := attachTestDocument(t, ctx, cli)

	// 02. Set up the user webhook server.
	requestCnt := atomic.NewInt32(0)
	userServer := newUserServer(t, requestCnt, project.SecretKey, docKey)
	defer userServer.Close()

	// 03. Register the event webhook.
	prj := registerWebhook(t, ctx, adminCli, project, userServer.URL, []string{string(types.DocRootChanged)})
	assert.Equal(t, userServer.URL, prj.EventWebhookURL)
	assert.Equal(t, string(types.DocRootChanged), prj.EventWebhookTypes[0])

	// 04. Test the DocRootChanged event is sent to the user server
	prev := requestCnt.Load()
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetCounter("counter").Increase(1)
		return nil
	}))
	assert.NoError(t, cli.Sync(ctx))
	assert.Equal(t, prev+1, requestCnt.Load())

	// 05. Unregister the event webhook.
	prj = registerWebhook(t, ctx, adminCli, project, userServer.URL, []string{})
	assert.Equal(t, userServer.URL, prj.EventWebhookURL)
	assert.Equal(t, 0, len(prj.EventWebhookTypes))

	// 06. Test the DocRootChanged event is not sent to the user server
	prev = requestCnt.Load()
	assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
		root.GetCounter("counter").Increase(1)
		return nil
	}))
	assert.NoError(t, cli.Sync(ctx))
	assert.Equal(t, prev, requestCnt.Load())
}

func TestDocRootChangedEventWebhook(t *testing.T) {
	ctx := context.Background()
	svr, adminCli := setupYorkieServer(t, "0ms")
	project, cli := createProjectAndClient(t, ctx, adminCli, svr.RPCAddr())

	t.Run("DocRootChanged event test", func(t *testing.T) {
		// 01. Attach a new document.
		doc, docKey := attachTestDocument(t, ctx, cli)

		// 02. Set up the user webhook server.
		requestCnt := atomic.NewInt32(0)
		userServer := newUserServer(t, requestCnt, project.SecretKey, docKey)
		defer userServer.Close()

		// 03. Register the event webhook.
		prj := registerWebhook(t, ctx, adminCli, project, userServer.URL, []string{string(types.DocRootChanged)})
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, string(types.DocRootChanged), prj.EventWebhookTypes[0])

		// 04. Update only root.
		prev := requestCnt.Load()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, prev+1, requestCnt.Load())

		// 05. Update only presence.
		prev = requestCnt.Load()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("update", "2")
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, prev, requestCnt.Load())

		// 06. Update both root and presence.
		prev = requestCnt.Load()
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("update", "3")
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		assert.Equal(t, prev+1, requestCnt.Load())
	})
}

func TestEventWebhookCache(t *testing.T) {
	cacheTTL := 10 * time.Millisecond
	ctx := context.Background()
	svr, adminCli := setupYorkieServer(t, cacheTTL.String())
	project, cli := createProjectAndClient(t, ctx, adminCli, svr.RPCAddr())

	t.Run("throttling event test", func(t *testing.T) {
		// 01. Attach a new document.
		doc, docKey := attachTestDocument(t, ctx, cli)

		// 02. Set up the user webhook server.
		requestCnt := atomic.NewInt32(0)
		userServer := newUserServer(t, requestCnt, project.SecretKey, docKey)
		defer userServer.Close()

		// 03. Register the event webhook.
		prj := registerWebhook(t, ctx, adminCli, project, userServer.URL, []string{string(types.DocRootChanged)})
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, string(types.DocRootChanged), prj.EventWebhookTypes[0])

		// 04. Test request throttled
		prevCount := requestCnt.Load()

		expectedUpdates := 5
		testDuration := cacheTTL * time.Duration(expectedUpdates)
		interval := cacheTTL / 10

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		timeCtx, cancel := context.WithTimeout(ctx, testDuration)
		defer cancel()

		for {
			select {
			case <-ticker.C:
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetCounter("counter").Increase(1)
					return nil
				}))
				assert.NoError(t, cli.Sync(ctx))
			case <-timeCtx.Done():
				assert.Equal(t, prevCount+int32(expectedUpdates), requestCnt.Load(), "Throttling did not trigger expected number of updates")
				return
			}
		}
	})
}
