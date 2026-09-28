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
	stderrors "errors"
	"io"
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
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// A write deadline must release a Watch handler even when its peer stops
// reading. The production HTTP server deliberately has no global timeout.
func TestWatchWriteDeadlineUnblocksSlowReader(t *testing.T) {
	const h2cProtocol = "h2c"
	for _, protocol := range []string{"http1", h2cProtocol} {
		t.Run(protocol, func(t *testing.T) {
			finished := make(chan error, 1)
			armed := make(chan error, 1)
			handler := withWatchWriteDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/other" {
					w.WriteHeader(http.StatusOK)
					return
				}
				setDeadline := r.Context().Value(watchDeadlineKey{}).(func(time.Time) error)
				// Reported separately from the write outcome: a transport that
				// cannot arm the deadline is a failure of the feature, not the
				// blocked write this test is looking for.
				err := setDeadline(time.Now().Add(200 * time.Millisecond))
				armed <- err
				if err != nil {
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
			select {
			case err := <-armed:
				require.NoError(t, err, "the Watch write deadline must be supported by the transport")
			case <-time.After(2 * time.Second):
				t.Fatal("the Watch handler never armed its write deadline")
			}
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

// On HTTP/1.1 the write deadline belongs to the connection, so a deadline the
// Watch handler leaves behind — or one its lease goroutine arms a moment after
// the handler returned — would fire on whatever request reuses that connection.
func TestWatchWriteDeadlineIsClearedForTheNextRequest(t *testing.T) {
	const chunk = 64 * 1024
	renewals := make(chan func(time.Time) error, 1)
	handler := withWatchWriteDeadline(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/Watch" {
			setDeadline := r.Context().Value(watchDeadlineKey{}).(func(time.Time) error)
			require.NoError(t, setDeadline(time.Now().Add(time.Hour)))
			renewals <- setDeadline
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes.Repeat([]byte("x"), chunk))
	}))
	srv := httptest.NewServer(handler)
	defer srv.Close()
	client := &http.Client{Transport: &http.Transport{}}

	response, err := client.Get(srv.URL + "/Watch")
	require.NoError(t, err)
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	// The lease goroutine can outlive its handler by a moment: a renewal that
	// lands then must become a no-op instead of arming a connection the
	// stream no longer owns.
	setDeadline := <-renewals
	require.NoError(t, setDeadline(time.Now().Add(-time.Hour)))

	sibling, err := client.Get(srv.URL + "/other")
	require.NoError(t, err, "a stale Watch deadline must not fire on the next request")
	body, err := io.ReadAll(sibling.Body)
	require.NoError(t, err, "a stale Watch deadline must not fire on the next request")
	require.Len(t, body, chunk)
	require.NoError(t, sibling.Body.Close())
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

		start := time.Now()
		require.Equal(t, start.Add(maxWatchLeaseAge), leaseExpiry(start), retries)
	}
}

// A deleted project is a denial; a lookup blip only leaves the project's
// current settings unknown, which the lease window resolves.
func TestWatchProjectReloadClassifiesFailures(t *testing.T) {
	deleted := watchProjectReloadError(database.ErrProjectNotFound)
	require.True(t, errors.IsStatus(deleted, errors.ErrCodePermissionDenied))
	require.EqualError(t, deleted, "watch project no longer exists")

	blip := watchProjectReloadError(stderrors.New("database unavailable"))
	require.False(t, errors.IsStatus(blip, errors.ErrCodePermissionDenied),
		"a transient lookup failure is unknown authorization, not a denial")
	require.ErrorContains(t, blip, "watch project authorization unavailable")
}

// An unconfirmed check must not drop a stream the lease window can still
// cover, nor keep one open past that window.
func TestWatchLeaseToleratesUnconfirmedChecks(t *testing.T) {
	ctx := logging.With(context.Background(), logging.DefaultLogger())
	interval := watchLeaseInterval()
	unknown := watchProjectReloadError(stderrors.New("database unavailable"))

	// A project that requires no authorization for this method has no window,
	// so a lookup blip cannot revoke what was never required.
	require.NoError(t, (&watchLease{}).tolerate(ctx, unknown, interval))

	// Room for another attempt: the stream rides out the blip.
	roomy := &watchLease{expires: time.Now().Add(interval + time.Second)}
	require.NoError(t, roomy.tolerate(ctx, unknown, interval))

	// No room left: the stream ends at its window rather than past it.
	expiring := &watchLease{expires: time.Now().Add(interval / 2)}
	require.ErrorIs(t, expiring.tolerate(ctx, unknown, interval), errWatchLeaseExpired)

	// A denial ends the stream at once, whatever the window has left.
	deleted := watchProjectReloadError(database.ErrProjectNotFound)
	require.ErrorIs(t, (&watchLease{}).tolerate(ctx, deleted, interval), deleted)
	require.ErrorIs(t, roomy.tolerate(ctx, auth.ErrPermissionDenied, interval), auth.ErrPermissionDenied)
}

// Without the transport hook a stream cannot be bounded at all, so admission
// and renewal both refuse it rather than leave it unbounded.
func TestWatchLeaseFailsClosedWithoutWriteDeadline(t *testing.T) {
	project := &types.Project{
		AuthWebhookURL:     "http://localhost:0",
		AuthWebhookMethods: []string{string(types.Watch)},
	}
	access := &types.AccessInfo{Method: types.Watch}
	require.True(t, project.RequireAuth(access.Method))

	s := &yorkieServer{}
	lease, err := s.startWatchLease(projects.With(context.Background(), project), access)
	require.Nil(t, lease)
	require.ErrorIs(t, err, auth.ErrPermissionDenied)

	require.ErrorIs(t, renewWatchLease(&watchLease{}, nil, time.Now()), auth.ErrPermissionDenied)
}

// A project that stops requiring authorization mid-stream must release the
// window its admission armed, not leave the stream to expire against it.
func TestWatchLeaseReleasesWindowWhenAuthStops(t *testing.T) {
	var cleared bool
	lease := &watchLease{expires: time.Now().Add(-time.Second)}
	require.ErrorIs(t, lease.maySend(), errWatchLeaseExpired)

	require.NoError(t, releaseWatchLease(lease, func(at time.Time) error {
		cleared = at.IsZero()
		return nil
	}))
	require.True(t, cleared, "the transport write deadline must be cleared too")
	require.NoError(t, lease.maySend())
}

// The re-check period must leave a full default webhook attempt inside the
// window it renews.
func TestWatchLeaseIntervalLeavesRoomForACheck(t *testing.T) {
	require.Equal(t, database.DefaultAuthWebhookRequestTimeout, watchLeaseCheckAllowance)
	require.Equal(t, maxWatchLeaseAge-watchLeaseCheckAllowance, watchLeaseInterval())
	require.Positive(t, watchLeaseInterval())
}

func TestWatchLeaseCheckKeepsEarlierExpiry(t *testing.T) {
	start := time.Now()
	lease := &watchLease{expires: start.Add(2 * time.Second)}
	require.Equal(t, lease.expires, lease.checkDeadline(start))
	lease.expires = time.Time{}
	require.Equal(t, start.Add(maxWatchLeaseAge), lease.checkDeadline(start))
}
