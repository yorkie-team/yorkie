//go:build linux && bench

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

package bench

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	pkgwebhook "github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend/webhook"
	"github.com/yorkie-team/yorkie/server/logging"
)

// setupWebhookServer simulates an HTTP server for the benchmark.
func setupWebhookServer(t *testing.B, count int) []*httptest.Server {
	servers := make([]*httptest.Server, 0, count)
	for range count {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.NotEmpty(t, r.Header.Get("X-Signature-256"))
			w.WriteHeader(http.StatusOK)
		}))
		servers = append(servers, server)
	}
	return servers
}

func BenchmarkWebhook(b *testing.B) {
	benches := []struct {
		endpointNum int
		webhookNum  int
	}{
		{endpointNum: 10, webhookNum: 10},
		{endpointNum: 10, webhookNum: 100},
		{endpointNum: 100, webhookNum: 10},
		{endpointNum: 100, webhookNum: 100},
		{endpointNum: 1000, webhookNum: 10},
	}

	for _, bench := range benches {
		tName := fmt.Sprintf(
			"Send %d Webhooks to %d Endpoints",
			bench.webhookNum,
			bench.endpointNum,
		)
		b.Run(tName, func(b *testing.B) {
			benchmarkSendWebhook(b, bench.webhookNum, bench.endpointNum)
		})
	}
}

func BenchmarkWebhookWithLimit(b *testing.B) {
	benches := []struct {
		endpointNum int
		webhookNum  int
	}{
		{endpointNum: 10, webhookNum: 10},
		{endpointNum: 10, webhookNum: 100},
		{endpointNum: 100, webhookNum: 10},
		{endpointNum: 100, webhookNum: 100},
		{endpointNum: 1000, webhookNum: 10},
	}

	for _, bench := range benches {
		tName := fmt.Sprintf(
			"Send %d Webhooks to %d Endpoints with limit",
			bench.webhookNum,
			bench.endpointNum,
		)
		b.Run(tName, func(b *testing.B) {
			benchmarkSendWebhookWithLimits(b, bench.webhookNum, bench.endpointNum)
		})
	}
}

func benchmarkSendWebhook(b *testing.B, webhookNum, endpointNum int) {
	b.ReportAllocs()
	const (
		docKey     = "doc-key"
		signingKey = "sign-key"
	)
	endpoints := setupWebhookServer(b, endpointNum)
	defer func() {
		for _, server := range endpoints {
			server.Close()
		}
	}()

	cli := pkgwebhook.NewClient[types.EventWebhookRequest, int](
		pkgwebhook.Options{
			MaxRetries:      0,
			MinWaitInterval: 100 * time.Millisecond,
			MaxWaitInterval: 100 * time.Millisecond,
			RequestTimeout:  100 * time.Millisecond,
		},
	)
	for range b.N {
		for range webhookNum {
			for i := range endpointNum {
				err := webhook.SendWebhook(
					context.Background(),
					cli,
					types.DocRootChanged,
					types.WebhookAttribute{
						DocKey:     docKey,
						SigningKey: signingKey,
						URL:        endpoints[i].URL,
					},
				)
				assert.NoError(b, err)
			}
		}
	}
}

func benchmarkSendWebhookWithLimits(b *testing.B, webhookNum, endpointNum int) {
	b.ReportAllocs()
	const (
		docKey     = "doc-key"
		signingKey = "sign-key"
	)

	endpoints := setupWebhookServer(b, endpointNum)
	defer func() {
		for _, server := range endpoints {
			server.Close()
		}
	}()

	cli := pkgwebhook.NewClient[types.EventWebhookRequest, int](
		pkgwebhook.Options{
			MaxRetries:      0,
			MinWaitInterval: 100 * time.Millisecond,
			MaxWaitInterval: 100 * time.Millisecond,
			RequestTimeout:  100 * time.Millisecond,
		},
	)

	logging.DefaultLogger()

	for range b.N {
		manager := webhook.NewManager(cli)
		for range webhookNum {
			for i := range endpointNum {
				err := manager.Send(
					context.Background(),
					types.NewEventWebhookInfo(
						types.DocRefKey{
							DocID:     types.ID(fmt.Sprintf("doc-id-%d", i)),
							ProjectID: "prj-id",
						},
						types.DocRootChanged,
						signingKey,
						endpoints[i].URL,
						docKey,
					),
				)
				assert.NoError(b, err)
			}
		}
		manager.Close()
	}
}
