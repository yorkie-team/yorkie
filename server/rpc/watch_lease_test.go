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
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend"
	backendcache "github.com/yorkie-team/yorkie/server/backend/cache"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
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

// The window must never cut a single legal webhook attempt short, and the
// re-check period must follow the operator's configured cache TTL rather than
// a hardcoded server policy.
func TestWatchLeaseWindowFollowsConfiguration(t *testing.T) {
	project := &types.Project{
		AuthWebhookRequestTimeout:  "7s",
		AuthWebhookMinWaitInterval: "100ms",
		AuthWebhookMaxWaitInterval: "3s",
		AuthWebhookMaxRetries:      10,
	}

	allowance, err := watchLeaseAllowance(project)
	require.NoError(t, err)
	require.Equal(t, 7*time.Second+watchLeaseMargin, allowance,
		"a single attempt the project's own timeout allows must fit")

	s := &yorkieServer{backend: &backend.Backend{Config: &backend.Config{AuthWebhookCacheTTL: "30s"}}}
	require.Equal(t, 30*time.Second, s.watchLeaseInterval())
	window, err := s.watchLeaseWindow(project)
	require.NoError(t, err)
	require.Equal(t, 30*time.Second+allowance, window)

	// A TTL shorter than the floor cannot turn the lease into a busy loop.
	s.backend.Config.AuthWebhookCacheTTL = time.Millisecond.String()
	require.Equal(t, minWatchLeaseInterval, s.watchLeaseInterval())

	// A saturating timeout must not wrap into a window that expires at once.
	project.AuthWebhookRequestTimeout = time.Duration(math.MaxInt64).String()
	window, err = s.watchLeaseWindow(project)
	require.NoError(t, err)
	require.Positive(t, window)
}

// An unconfirmed check ends the stream retriably: nothing denied this client,
// so the SDK must re-establish the watch rather than treat it as refused.
func TestWatchLeaseExpiryIsRetriable(t *testing.T) {
	require.True(t, errors.IsStatus(errWatchLeaseExpired, errors.ErrCodeUnavailable))
	require.False(t, errors.IsStatus(errWatchLeaseExpired, errors.ErrCodePermissionDenied))
}

// A terminal failure must reach a handler already blocked in send(), including
// one whose project required no authorization when the stream opened.
func TestWatchLeaseFailureBoundsBlockedSend(t *testing.T) {
	armed := make(chan time.Time, 1)
	lease := &watchLease{
		failure:     make(chan error, 1),
		setDeadline: func(at time.Time) error { armed <- at; return nil },
	}

	lease.fail(errWatchLeaseExpired)

	require.ErrorIs(t, <-lease.failure, errWatchLeaseExpired)
	require.ErrorIs(t, lease.maySend(), errWatchLeaseExpired)
	select {
	case at := <-armed:
		require.False(t, at.IsZero(), "a blocked write must be bounded by the failure")
		require.WithinDuration(t, time.Now().Add(watchLeaseMargin), at, time.Second)
	default:
		t.Fatal("a terminal lease failure left the transport unbounded")
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
	const interval = 2 * time.Second
	window := interval + time.Second
	noop := func(time.Time) error { return nil }
	unknown := watchProjectReloadError(stderrors.New("database unavailable"))

	// A lease with no window yet must not stay unbounded through uncertainty:
	// tolerating the blip arms a window the next blip can run down.
	unarmed := &watchLease{window: window, setDeadline: noop}
	start := time.Now()
	require.NoError(t, unarmed.tolerate(ctx, unknown, interval, start))
	require.Equal(t, start.Add(window), unarmed.expires)
	// Once that window has no room for another attempt the stream ends, rather
	// than failing open for as long as the uncertainty lasts.
	unarmed.expires = time.Now().Add(interval / 2)
	require.ErrorIs(t, unarmed.tolerate(ctx, unknown, interval, time.Now()), errWatchLeaseExpired)

	// Room for another attempt: the stream rides out the blip.
	roomy := &watchLease{expires: time.Now().Add(interval + time.Second)}
	require.NoError(t, roomy.tolerate(ctx, unknown, interval, time.Now()))

	// No room left: the stream ends at its window rather than past it.
	expiring := &watchLease{expires: time.Now().Add(interval / 2)}
	require.ErrorIs(t, expiring.tolerate(ctx, unknown, interval, time.Now()), errWatchLeaseExpired)

	// A denial ends the stream at once, whatever the window has left.
	deleted := watchProjectReloadError(database.ErrProjectNotFound)
	blank := &watchLease{window: window, setDeadline: noop}
	require.ErrorIs(t, blank.tolerate(ctx, deleted, interval, time.Now()), deleted)
	require.ErrorIs(t, roomy.tolerate(ctx, auth.ErrPermissionDenied, interval, time.Now()),
		auth.ErrPermissionDenied)
}

// Without the transport hook a stream cannot be bounded at all, so admission
// refuses it and a mid-stream renewal ends it rather than leaving it unbounded.
func TestWatchLeaseFailsClosedWithoutWriteDeadline(t *testing.T) {
	project := &types.Project{
		AuthWebhookURL:             "http://localhost:0",
		AuthWebhookMethods:         []string{string(types.Watch)},
		AuthWebhookMinWaitInterval: "100ms",
		AuthWebhookMaxWaitInterval: "3s",
		AuthWebhookRequestTimeout:  "3s",
	}
	access := &types.AccessInfo{Method: types.Watch}
	require.True(t, project.RequireAuth(access.Method))

	s := &yorkieServer{backend: &backend.Backend{Config: &backend.Config{AuthWebhookCacheTTL: "10s"}}}
	lease, err := s.startWatchLease(projects.With(context.Background(), project), access)
	require.Nil(t, lease)
	require.ErrorIs(t, err, auth.ErrPermissionDenied)

	require.ErrorIs(t, (&watchLease{window: time.Second}).arm(time.Now()), errWatchLeaseExpired)
}

// A deployment with no auth webhook has nothing to revoke, so its streams must
// not pay for a lease goroutine, a ticker or a project reload.
func TestWatchLeaseIsNotStartedWithoutAnAuthWebhook(t *testing.T) {
	s := &yorkieServer{}
	lease, err := s.startWatchLease(
		projects.With(context.Background(), &types.Project{}),
		&types.AccessInfo{Method: types.Watch},
	)
	require.NoError(t, err)
	require.NoError(t, lease.maySend())
	require.Nil(t, lease.cancel, "an unprotected project must not run a lease goroutine")
	require.Nil(t, lease.failure)
	lease.stop()
}

// A project that stops requiring authorization mid-stream must release the
// window its admission armed, not leave the stream to expire against it.
func TestWatchLeaseReleasesWindowWhenAuthStops(t *testing.T) {
	var cleared bool
	lease := &watchLease{expires: time.Now().Add(-time.Second)}
	lease.setDeadline = func(at time.Time) error {
		cleared = at.IsZero()
		return nil
	}
	require.ErrorIs(t, lease.maySend(), errWatchLeaseExpired)

	require.NoError(t, lease.release())
	require.True(t, cleared, "the transport write deadline must be cleared too")
	require.NoError(t, lease.maySend())
}

func TestWatchLeaseCheckKeepsEarlierExpiry(t *testing.T) {
	start := time.Now()
	lease := &watchLease{window: 5 * time.Second, expires: start.Add(2 * time.Second)}
	require.Equal(t, lease.expires, lease.checkDeadline(start))
	lease.expires = time.Time{}
	require.Equal(t, start.Add(lease.window), lease.checkDeadline(start))
}

// watchLeaseProjectDB returns the settings observed when an unprotected stream
// first discovers that its project now requires authorization.
type watchLeaseProjectDB struct {
	database.Database
	project *database.ProjectInfo
}

func (d *watchLeaseProjectDB) FindProjectInfoByPublicKey(
	_ context.Context, _ string,
) (*database.ProjectInfo, error) {
	return d.project.DeepCopy(), nil
}

func TestWatchLeaseArmsBeforeNewlyRequiredAuthorization(t *testing.T) {
	for _, method := range []types.Method{types.Watch, types.WatchDocument, types.WatchChannel} {
		t.Run(string(method), func(t *testing.T) {
			for _, response := range []string{"unavailable", "malformed", "timeout"} {
				t.Run(response, func(t *testing.T) {
					for _, armed := range []bool{false, true} {
						name := "unarmed"
						if armed {
							name = "existing"
						}
						t.Run(name, func(t *testing.T) {
							deadlines := make(chan time.Time, 4)
							deadlineAtCheck := make(chan time.Time, 1)
							hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								_, _ = io.Copy(io.Discard, r.Body)
								var expiry time.Time
								select {
								case expiry = <-deadlines:
								default:
								}
								deadlineAtCheck <- expiry
								switch response {
								case "unavailable":
									w.WriteHeader(http.StatusServiceUnavailable)
								case "malformed":
									_, _ = w.Write([]byte("invalid JSON"))
								case "timeout":
									<-r.Context().Done()
								}
							}))
							defer hook.Close()
							project := database.NewProjectInfo(t.Name(), database.ZeroID)
							project.PublicKey = "synthetic-project"
							project.AuthWebhookURL = hook.URL
							project.AuthWebhookMethods = []string{string(method)}
							project.AuthWebhookMaxRetries = 0
							project.AuthWebhookMinWaitInterval = "1ms"
							project.AuthWebhookMaxWaitInterval = "1ms"
							project.AuthWebhookRequestTimeout = "50ms"
							caches, err := backendcache.New(backendcache.Options{
								AuthWebhookCacheSize: 1, AuthWebhookCacheTTL: 10 * time.Second,
								SnapshotCacheSize: 1, ChannelSessionCountCacheSize: 1,
								ChannelSessionCountCacheTTL: time.Second,
							})
							require.NoError(t, err)
							be := &backend.Backend{
								DB: &watchLeaseProjectDB{project: project}, Cache: caches,
								Config:            &backend.Config{AuthWebhookCacheTTL: "1s"},
								AuthWebhookClient: webhook.NewClient[types.AuthWebhookRequest, types.AuthWebhookResponse](false),
							}
							defer be.AuthWebhookClient.Close()
							s := &yorkieServer{backend: be}
							setDeadline := func(at time.Time) error { deadlines <- at; return nil }
							interval := s.watchLeaseInterval()
							window, err := s.watchLeaseWindow(project.ToProject())
							require.NoError(t, err)
							lease := &watchLease{window: window, setDeadline: setDeadline}
							var previous time.Time
							if armed {
								previous = time.Now().Add(4 * time.Second)
								lease.expires = previous
								deadlines <- previous
							}
							ctx := metadata.With(logging.With(context.Background(), logging.DefaultLogger()),
								metadata.Metadata{APIKey: project.PublicKey, Authorization: "synthetic-token"})
							access := &types.AccessInfo{Method: method}
							start := time.Now()
							require.NoError(t, s.recheckWatchLease(ctx, lease, access, interval, project.PublicKey))
							observed := <-deadlineAtCheck
							require.False(t, observed.IsZero(), "the transport must be bounded before calling the newly required webhook")
							require.Equal(t, observed, lease.expires)
							if armed {
								require.Equal(t, previous, lease.expires)
							} else {
								require.WithinDuration(t, start.Add(window), lease.expires, time.Second)
							}
							firstExpiry := lease.expires
							require.NoError(t, s.recheckWatchLease(ctx, lease, access, interval, project.PublicKey))
							<-deadlineAtCheck
							require.Equal(t, firstExpiry, lease.expires, "uncertain checks must not extend the lease")
						})
					}
				})
			}
		})
	}
}
