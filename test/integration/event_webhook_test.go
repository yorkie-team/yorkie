//go:build integration

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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

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

func newWebhookServer(t *testing.T, secretKey, docKey string) (*httptest.Server, *int32) {
	var reqCnt int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&reqCnt, 1)
		signatureHeader := r.Header.Get("X-Signature-256")
		assert.NotZero(t, len(signatureHeader))
		body, err := io.ReadAll(r.Body)
		assert.NoError(t, err)
		assert.NoError(t, verifySignature(signatureHeader, secretKey, body))

		req := &types.EventWebhookRequest{}
		assert.NoError(t, gojson.Unmarshal(body, req))
		assert.Equal(t, types.DocRootChanged, req.Type)
		assert.Equal(t, docKey, req.Attributes.Key)

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { srv.Close() })

	return srv, &reqCnt
}

// newYorkieServer initializes the Yorkie server and admin client.
func newYorkieServer(t *testing.T, webhookCacheTTL, projectCacheTTL string) *server.Yorkie {
	conf := helper.TestConfig()
	if webhookCacheTTL != "default" {
		conf.Backend.EventWebhookCacheTTL = webhookCacheTTL
	}
	if projectCacheTTL != "default" {
		conf.Backend.ProjectCacheTTL = projectCacheTTL
	}
	svr, err := server.New(conf)
	assert.NoError(t, err)
	assert.NoError(t, svr.Start())

	t.Cleanup(func() {
		assert.NoError(t, svr.Shutdown(true))
	})

	return svr
}

func newActivatedClient(t *testing.T, ctx context.Context, addr, publicKey string) *client.Client {
	cli, err := client.Dial(addr, client.WithAPIKey(publicKey))
	assert.NoError(t, err)
	assert.NoError(t, cli.Activate(ctx))
	t.Cleanup(func() {
		assert.NoError(t, cli.Deactivate(ctx))
		assert.NoError(t, cli.Close())
	})
	return cli
}

func TestRegisterEventWebhook(t *testing.T) {
	ctx := context.Background()

	// Set up yorkie server
	projectCacheTTL := 1 * time.Millisecond
	svr := newYorkieServer(t, "0ms", projectCacheTTL.String())

	// Set up project
	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())
	defer func() { adminCli.Close() }()

	project, err := adminCli.CreateProject(ctx, "register-event-webhook")
	assert.NoError(t, err)

	doc := document.New(helper.TestDocKey(t))
	userServer, getReqCnt := newWebhookServer(t, project.SecretKey, doc.Key().String())

	cli := newActivatedClient(t, ctx, svr.RPCAddr(), project.PublicKey)

	assert.NoError(t, cli.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
		"counter": json.NewCounter(0, crdt.LongCnt),
	})))

	waitWebhookReceived := 10 * time.Millisecond

	t.Run("register event webhook test", func(t *testing.T) {
		// 01. Register event webhook
		prj, err := adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
			EventWebhookURL:   &userServer.URL,
			EventWebhookTypes: &[]string{string(types.DocRootChanged)},
		})
		assert.NoError(t, err)
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, string(types.DocRootChanged), prj.EventWebhookTypes[0])

		// 02. Wait project cache expired
		time.Sleep(projectCacheTTL)

		// 03. Check webhook received
		prev := atomic.LoadInt32(getReqCnt)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		time.Sleep(waitWebhookReceived)
		assert.Equal(t, prev+1, atomic.LoadInt32(getReqCnt))
	})

	t.Run("unregister event webhook test", func(t *testing.T) {
		// 01. Unregister event webhook
		prj, err := adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
			EventWebhookURL:   &userServer.URL,
			EventWebhookTypes: &[]string{},
		})
		assert.NoError(t, err)
		assert.Equal(t, userServer.URL, prj.EventWebhookURL)
		assert.Equal(t, 0, len(prj.EventWebhookTypes))

		// 02. Wait project cache expired
		time.Sleep(projectCacheTTL)

		// 03. Check webhook doesn't trigger
		prev := atomic.LoadInt32(getReqCnt)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))

		// 04. Wait webhook received
		time.Sleep(waitWebhookReceived)
		assert.Equal(t, prev, atomic.LoadInt32(getReqCnt))
	})
}

func TestDocRootChangedEventWebhook(t *testing.T) {
	ctx := context.Background()

	svr := newYorkieServer(t, "0ms", "default")
	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())

	project, err := adminCli.CreateProject(ctx, "doc-root-changed-event-webhook")
	assert.NoError(t, err)

	doc := document.New(helper.TestDocKey(t))
	userServer, getReqCnt := newWebhookServer(t, project.SecretKey, doc.Key().String())

	project.EventWebhookURL = userServer.URL
	_, err = adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		EventWebhookURL:   &project.EventWebhookURL,
		EventWebhookTypes: &[]string{string(types.DocRootChanged)},
	})
	assert.NoError(t, err)

	cli := newActivatedClient(t, ctx, svr.RPCAddr(), project.PublicKey)

	assert.NoError(t, cli.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
		"counter": json.NewCounter(0, crdt.LongCnt),
	})))

	waitWebhookReceived := 10 * time.Millisecond
	t.Run("root element changed test", func(t *testing.T) {
		prev := atomic.LoadInt32(getReqCnt)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		time.Sleep(waitWebhookReceived)
		assert.Equal(t, prev+1, atomic.LoadInt32(getReqCnt))
	})

	t.Run("presence changed test", func(t *testing.T) {
		prev := atomic.LoadInt32(getReqCnt)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("update", "2")
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		time.Sleep(waitWebhookReceived)
		assert.Equal(t, prev, atomic.LoadInt32(getReqCnt))
	})

	t.Run("root element and presence changed test", func(t *testing.T) {
		prev := atomic.LoadInt32(getReqCnt)
		assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
			p.Set("update", "3")
			root.GetCounter("counter").Increase(1)
			return nil
		}))
		assert.NoError(t, cli.Sync(ctx))
		time.Sleep(waitWebhookReceived)
		assert.Equal(t, prev+1, atomic.LoadInt32(getReqCnt))
	})
}

func TestEventWebhookCache(t *testing.T) {
	ctx := context.Background()

	webhookCacheTTL := 10 * time.Millisecond
	svr := newYorkieServer(t, webhookCacheTTL.String(), "default")
	adminCli := helper.CreateAdminCli(t, svr.RPCAddr())

	project, err := adminCli.CreateProject(ctx, "event-webhook-cache-webhook")
	assert.NoError(t, err)

	doc := document.New(helper.TestDocKey(t))
	userServer, getReqCnt := newWebhookServer(t, project.SecretKey, doc.Key().String())
	_, err = adminCli.UpdateProject(ctx, project.ID.String(), &types.UpdatableProjectFields{
		EventWebhookURL:   &userServer.URL,
		EventWebhookTypes: &[]string{string(types.DocRootChanged)},
	})
	assert.NoError(t, err)

	cli := newActivatedClient(t, ctx, svr.RPCAddr(), project.PublicKey)
	assert.NoError(t, cli.Attach(ctx, doc, client.WithInitialRoot(map[string]any{
		"counter": json.NewCounter(0, crdt.LongCnt),
	})))

	waitWebhookReceived := 20 * time.Millisecond
	t.Run("throttling event test", func(t *testing.T) {
		t.Skip("remove this after implement advanced event cache control")

		expectedUpdates := 5
		testDuration := webhookCacheTTL * time.Duration(expectedUpdates)
		interval := webhookCacheTTL / 10

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		timeCtx, cancel := context.WithTimeout(ctx, testDuration)
		defer cancel()

		prevCnt := atomic.LoadInt32(getReqCnt)
		for {
			select {
			case <-ticker.C:
				assert.NoError(t, doc.Update(func(root *json.Object, p *presence.Presence) error {
					root.GetCounter("counter").Increase(1)
					return nil
				}))
				assert.NoError(t, cli.Sync(ctx))
			case <-timeCtx.Done():
				time.Sleep(waitWebhookReceived)
				assert.Equal(t, prevCnt+int32(expectedUpdates), atomic.LoadInt32(getReqCnt))
				return
			}
		}
	})
}
