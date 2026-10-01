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
	"context"
	stderrors "errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

const (
	// minWatchLeaseInterval floors the re-check period so a very short cache
	// TTL cannot turn every stream into a busy loop.
	minWatchLeaseInterval = time.Second

	// watchLeaseMargin is slack added to the room a single re-check has, so a
	// check that is merely slow, rather than denied, is not cut off by the
	// window a hair before its own request timeout would have answered.
	watchLeaseMargin = time.Second
)

type watchDeadlineKey struct{}

// errWatchLeaseExpired ends a stream whose authorization could not be
// confirmed inside its window. It is deliberately Unavailable rather than
// PermissionDenied: nothing denied this client, the server merely stopped
// being able to vouch for it, and the SDK convention for a server-side stream
// termination the client never asked for is a retriable status (see
// ErrSubscriptionsClosed in docs/design/pub-sub.md).
var errWatchLeaseExpired = errors.Unavailable("watch authorization lease expired")

type watchLease struct {
	failure     chan error
	cancel      context.CancelFunc
	done        chan struct{}
	setDeadline func(time.Time) error
	mu          sync.RWMutex
	window      time.Duration
	expires     time.Time
	terminal    error
}

func (l *watchLease) maySend() error {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.terminal != nil {
		return l.terminal
	}
	if !l.expires.IsZero() && !time.Now().Before(l.expires) {
		return errWatchLeaseExpired
	}
	return nil
}

// checkDeadline limits a recheck to the earlier of the current lease expiry
// and one whole window from this check's start.
func (l *watchLease) checkDeadline(start time.Time) time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	deadline := start.Add(l.window)
	if !l.expires.IsZero() && l.expires.Before(deadline) {
		return l.expires
	}
	return deadline
}

func (l *watchLease) stop() {
	if l.cancel != nil {
		l.cancel()
		<-l.done
	}
}

// fail records the error that ends the stream and hands it to the handler.
//
// The write deadline is pulled in as well: a handler already blocked in send()
// never reaches maySend again, and a stream whose project required no
// authorization when it opened may have no deadline armed at all, so without
// this the failure would sit in the channel while the stream kept hanging. The
// deadline is one margin out rather than now, so a handler that is not blocked
// still has room to write the status that tells the client why it ended.
func (l *watchLease) fail(err error) {
	l.mu.Lock()
	l.terminal = err
	l.mu.Unlock()

	if l.setDeadline != nil {
		_ = l.setDeadline(time.Now().Add(watchLeaseMargin))
	}

	l.failure <- err
}

// withWatchWriteDeadline bounds a blocked HTTP stream write even when its
// handler cannot return promptly. Only Watch procedures get a renewable write
// deadline; other RPCs keep their existing transport behavior.
func withWatchWriteDeadline(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if !strings.HasSuffix(path, "/Watch") &&
			!strings.HasSuffix(path, "/WatchDocument") &&
			!strings.HasSuffix(path, "/WatchChannel") {
			next.ServeHTTP(w, r)
			return
		}

		// On HTTP/1.1 the write deadline belongs to the connection rather
		// than the request, so a deadline left behind would fire on
		// whatever request next reuses the keep-alive connection. It is
		// cleared here rather than relying on net/http clearing it after
		// finishRequest (net/http/server.go), which is not part of the
		// documented contract of ResponseController. The mutex serializes
		// the lease goroutine's renewals against that reset, so no renewal
		// can re-arm a deadline after the handler has returned.
		controller := http.NewResponseController(w)
		var mu sync.Mutex
		served := false
		deadline := func(at time.Time) error {
			mu.Lock()
			defer mu.Unlock()
			if served {
				return nil
			}
			return controller.SetWriteDeadline(at)
		}
		defer func() {
			mu.Lock()
			defer mu.Unlock()
			served = true
			_ = controller.SetWriteDeadline(time.Time{})
		}()

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), watchDeadlineKey{}, deadline)))
	})
}

// watchLeaseInterval is how often an established stream is re-checked. It is
// the operator's own AuthWebhookCacheTTL: a decision older than that TTL is not
// served to any other RPC either, so re-checking at that period asks the
// project's webhook no more often than the configured cache already permits.
// Raising the TTL to shield a rate-limited webhook therefore also widens this
// period, instead of being silently overridden by a server policy.
func (s *yorkieServer) watchLeaseInterval() time.Duration {
	return max(s.backend.Config.ParseAuthWebhookCacheTTL(), minWatchLeaseInterval)
}

// watchLeaseWindow is how long a stream may keep delivering events after its
// authorization was last confirmed: one re-check period plus the room a single
// re-check has to answer. Revocation is therefore bounded by the operator's own
// cache TTL rather than by a hardcoded cutoff.
func (s *yorkieServer) watchLeaseWindow(project *types.Project) (time.Duration, error) {
	allowance, err := watchLeaseAllowance(project)
	if err != nil {
		return 0, err
	}

	return addDuration(s.watchLeaseInterval(), allowance), nil
}

// watchLeaseAllowance is the room a single re-check has to answer: the
// project's own per-request webhook timeout plus slack.
//
// The project's retry budget is deliberately not stacked inside one window:
// the lease's next tick is its retry, so an attempt that fails transiently is
// repeated a period later instead of consuming the whole window. What the
// window must never cut short is a single attempt the project's own timeout
// considers legal.
func watchLeaseAllowance(project *types.Project) (time.Duration, error) {
	options, err := project.GetAuthWebhookOptions()
	if err != nil {
		return 0, fmt.Errorf("watch lease allowance: %w", err)
	}

	return addDuration(options.RequestTimeout, watchLeaseMargin), nil
}

// addDuration returns a+b for non-negative durations, saturating at the
// largest representable duration.
func addDuration(a, b time.Duration) time.Duration {
	a, b = max(a, 0), max(b, 0)
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// startWatchLease checks an established stream against the project's current
// authorization settings. A denial stops the stream; the write deadline
// prevents a slow send from outliving the bounded lease.
//
// A project with no auth webhook at all — the default deployment — has nothing
// to revoke, so it gets no goroutine, no ticker and no project reload. Once a
// webhook is configured the lease runs even for methods it does not cover
// today, so a project that widens its webhook afterwards still cuts off streams
// opened before.
//
// The window runs from the caller's admission check returning, not from when
// it started: an admission that legally spent the project's whole retry budget
// has still only just confirmed authorization, and must not be rejected as
// stale by the lease that is meant to carry it.
func (s *yorkieServer) startWatchLease(
	ctx context.Context,
	access *types.AccessInfo,
) (*watchLease, error) {
	project := projects.From(ctx)
	if project.AuthWebhookURL == "" {
		return &watchLease{}, nil
	}

	window, err := s.watchLeaseWindow(project)
	if err != nil {
		return nil, err
	}

	setDeadline, _ := ctx.Value(watchDeadlineKey{}).(func(time.Time) error)
	lease := &watchLease{setDeadline: setDeadline, window: window}

	if project.RequireAuth(access.Method) {
		if setDeadline == nil {
			return nil, auth.ErrPermissionDenied
		}
		if err := lease.arm(time.Now()); err != nil {
			return nil, err
		}
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	lease.cancel = cancel
	lease.failure = make(chan error, 1)
	lease.done = make(chan struct{})
	// Read on the handler goroutine: a panic here is recovered by net/http,
	// whereas one in the detached lease goroutine would take the server down.
	apiKey := metadata.From(ctx).APIKey
	go s.runWatchLease(leaseCtx, lease, access, s.watchLeaseInterval(), apiKey)

	return lease, nil
}

// runWatchLease re-checks the stream once per interval until the stream ends,
// the server stops, or a check refuses it.
func (s *yorkieServer) runWatchLease(
	leaseCtx context.Context,
	lease *watchLease,
	access *types.AccessInfo,
	interval time.Duration,
	apiKey string,
) {
	defer close(lease.done)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-leaseCtx.Done():
			return
		case <-s.serviceCtx.Done():
			return
		case <-ticker.C:
		}

		if err := s.recheckWatchLease(leaseCtx, lease, access, interval, apiKey); err != nil {
			if leaseCtx.Err() != nil {
				return
			}
			lease.fail(err)
			return
		}
	}
}

// recheckWatchLease runs one re-check and returns an error only when the
// stream must end: a denial ends it at once, while a result that merely leaves
// authorization unconfirmed is tolerated for as long as the lease window has
// room for another attempt.
func (s *yorkieServer) recheckWatchLease(
	leaseCtx context.Context,
	lease *watchLease,
	access *types.AccessInfo,
	interval time.Duration,
	apiKey string,
) error {
	checkStart := time.Now()
	checkCtx, stop := context.WithDeadline(leaseCtx, lease.checkDeadline(checkStart))
	defer stop()

	// The project captured when the stream opened is a snapshot. Re-read it
	// so that an auth webhook enabled or widened afterwards reaches streams
	// that are already established.
	fresh, err := projects.GetProjectFromAPIKey(checkCtx, s.backend, apiKey)
	if err != nil {
		return lease.tolerate(leaseCtx, watchProjectReloadError(err), interval, checkStart)
	}

	// The window follows the project's current settings, so a webhook request
	// timeout raised mid-stream widens the room its checks have rather than
	// being cut short by the window the stream opened with.
	window, err := s.watchLeaseWindow(fresh)
	if err != nil {
		return lease.tolerate(leaseCtx, err, interval, checkStart)
	}
	lease.mu.Lock()
	lease.window = window
	unarmed := lease.expires.IsZero()
	lease.mu.Unlock()

	// Newly required authorization must have a finite window even if the first
	// webhook check is uncertain. Preserve an armed window until a check succeeds.
	if fresh.RequireAuth(access.Method) && unarmed {
		if err := lease.arm(checkStart); err != nil {
			return err
		}
	}

	if err := s.checkWatchLease(checkCtx, fresh, access); err != nil {
		return lease.tolerate(leaseCtx, err, interval, checkStart)
	}

	if err := lease.maySend(); err != nil {
		return err
	}
	if !fresh.RequireAuth(access.Method) {
		return lease.release()
	}

	return lease.arm(checkStart)
}

// release lifts the bound from a stream whose project has stopped requiring
// authorization for this method. Without it a webhook removed or narrowed
// mid-stream would leave the admission window and its write deadline armed with
// nothing left to renew them, expiring streams that are once again allowed
// unconditionally.
func (l *watchLease) release() error {
	if l.setDeadline != nil {
		if err := l.setDeadline(time.Time{}); err != nil {
			return err
		}
	}

	l.mu.Lock()
	l.expires = time.Time{}
	l.mu.Unlock()

	return nil
}

// tolerate decides what an unconfirmed check means for the stream. A denial
// ends it immediately. Anything else — a lookup blip, a webhook outage —
// leaves current authorization unknown rather than revoked: the stream
// survives while another attempt still fits inside its window, and ends when
// no attempt does.
//
// A lease with no window yet is one whose project required no authorization
// for this method at the last confirmed check. Uncertainty must not leave such
// a stream unbounded forever, so a window is armed from this check instead: the
// stream rides out the blip and ends if the uncertainty outlasts the window.
func (l *watchLease) tolerate(
	ctx context.Context,
	cause error,
	interval time.Duration,
	checkStart time.Time,
) error {
	if errors.IsStatus(cause, errors.ErrCodePermissionDenied) ||
		errors.IsStatus(cause, errors.ErrCodeUnauthenticated) {
		return cause
	}

	l.mu.RLock()
	expires := l.expires
	l.mu.RUnlock()

	if expires.IsZero() {
		if err := l.arm(checkStart); err != nil {
			return err
		}
		logging.From(ctx).Warnf("watch lease: authorization unconfirmed, bounding stream: %v", cause)
		return nil
	}

	if time.Now().Add(interval).Before(expires) {
		logging.From(ctx).Warnf("watch lease: authorization unconfirmed, retrying: %v", cause)
		return nil
	}

	logging.From(ctx).Warnf("watch lease: authorization unconfirmed within the window: %v", cause)
	return errWatchLeaseExpired
}

// watchProjectReloadError classifies a failed project reload. A project that
// no longer exists is a denial; any other failure only leaves the current
// settings unknown, which tolerate resolves against the lease window.
func watchProjectReloadError(err error) error {
	if stderrors.Is(err, database.ErrProjectNotFound) {
		return errors.PermissionDenied("watch project no longer exists")
	}
	return fmt.Errorf("watch project authorization unavailable: %w", err)
}

// checkWatchLease asks the auth webhook whether the stream is still allowed.
// The project's own request timeout bounds the call; the lease window bounds it
// further through ctx, so a check that cannot answer inside the window does not
// renew the lease instead of running past the cutoff.
func (s *yorkieServer) checkWatchLease(
	ctx context.Context,
	project *types.Project,
	access *types.AccessInfo,
) error {
	if !project.RequireAuth(access.Method) {
		return nil
	}

	allowance, err := watchLeaseAllowance(project)
	if err != nil {
		return err
	}

	checkCtx, stop := context.WithTimeout(ctx, allowance)
	defer stop()

	return auth.VerifyWatchLease(checkCtx, s.backend, project, access)
}

// arm extends the stream's window from the moment its authorization was
// confirmed, bounding the transport write with the same instant.
func (l *watchLease) arm(confirmedAt time.Time) error {
	l.mu.RLock()
	window := l.window
	l.mu.RUnlock()

	// A project that starts requiring auth mid-stream cannot be bounded
	// without the transport hook, so such a stream ends rather than running
	// on unbounded. Nothing denied this client, so it ends retriably.
	if l.setDeadline == nil {
		return errWatchLeaseExpired
	}

	expiry := confirmedAt.Add(window)
	if !time.Now().Before(expiry) {
		return errWatchLeaseExpired
	}
	if err := l.setDeadline(expiry); err != nil {
		return err
	}

	l.mu.Lock()
	l.expires = expiry
	l.mu.Unlock()

	return nil
}
