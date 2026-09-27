/*
 * Copyright 2026 The Yorkie Authors. All rights reserved.
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

package rpc

import (
	"bytes"
	"context"
	"crypto/tls"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/yorkie-team/yorkie/api/types"
)

// A write deadline must release a Watch handler even when its peer stops
// reading. The production HTTP server deliberately has no global timeout.
func TestWatchWriteDeadlineUnblocksSlowReader(t *testing.T) {
	const h2cProtocol = "h2c"
	for _, protocol := range []string{"http1", h2cProtocol} {
		t.Run(protocol, func(t *testing.T) {
			finished := make(chan error, 1)
			handler := withWatchWriteDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/other" {
					w.WriteHeader(http.StatusOK)
					return
				}
				setDeadline := r.Context().Value(watchDeadlineKey{}).(func(time.Time) error)
				if err := setDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
					finished <- err
					return
				}
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				chunk := bytes.Repeat([]byte("x"), 64*1024)
				for range 1024 {
					if _, err := w.Write(chunk); err != nil {
						finished <- err
						return
					}
				}
				finished <- nil
			}))
			if protocol == h2cProtocol {
				handler = h2c.NewHandler(handler, &http2.Server{})
			}
			srv := httptest.NewServer(handler)
			defer srv.Close()
			transport := &http.Transport{}
			if protocol == h2cProtocol {
				transport = nil
			}
			var client *http.Client
			if protocol == h2cProtocol {
				client = &http.Client{Transport: &http2.Transport{
					AllowHTTP: true,
					DialTLSContext: func(ctx context.Context, _, addr string, _ *tls.Config) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, "tcp", addr)
					},
				}}
			} else {
				client = &http.Client{Transport: transport}
			}
			response, err := client.Get(srv.URL + "/Watch")
			require.NoError(t, err)
			defer func() { require.NoError(t, response.Body.Close()) }()
			siblingCtx, cancelSibling := context.WithTimeout(context.Background(), time.Second)
			defer cancelSibling()
			sibling, err := client.Do(mustRequest(t, siblingCtx, srv.URL+"/other"))
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, sibling.StatusCode)
			require.NoError(t, sibling.Body.Close())
			select {
			case err := <-finished:
				require.Error(t, err, "a non-reading peer should block until the write deadline")
			case <-time.After(2 * time.Second):
				t.Fatal("a blocked Watch send outlived its write deadline")
			}
		})
	}
}

func mustRequest(t *testing.T, ctx context.Context, url string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	require.NoError(t, err)
	return r
}

// A project may set any retry count; the lease budget must stay positive so
// a large one never expires every stream on its first check.
func TestWebhookBudgetSaturates(t *testing.T) {
	project := &types.Project{
		AuthWebhookRequestTimeout:  "3s",
		AuthWebhookMinWaitInterval: "100ms",
		AuthWebhookMaxWaitInterval: "3s",
	}

	project.AuthWebhookMaxRetries = 2
	budget, err := webhookBudget(project)
	require.NoError(t, err)
	require.Equal(t, 3*3*time.Second+100*time.Millisecond+200*time.Millisecond, budget)

	for _, retries := range []uint64{38, 100, math.MaxUint64} {
		project.AuthWebhookMaxRetries = retries
		budget, err := webhookBudget(project)
		require.NoError(t, err)
		require.Positive(t, budget, retries)

		expiry, err := leaseExpiry(project, time.Now(), time.Second)
		require.NoError(t, err)
		require.True(t, expiry.After(time.Now()), retries)
	}
}
