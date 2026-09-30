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
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/backend/database"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

const (
	// maxWatchLeaseAge bounds how long a stream may keep delivering events
	// after the last time its authorization was confirmed. It is a server
	// policy: a deployment answers for revocation within this window no
	// matter how long its admission cache TTL is.
	maxWatchLeaseAge = 5 * time.Second

	// watchLeaseCheckAllowance is the part of the window reserved for a
	// re-check to answer. It is the default per-request webhook timeout, so a
	// single default attempt fits; a project whose webhook needs longer than
	// the allowance does not renew on that attempt, and the lease retries on
	// its next tick rather than letting the retry policy run past the cutoff.
	watchLeaseCheckAllowance = 3 * time.Second

	// minWatchLeaseInterval floors the re-check period so a short window
	// cannot turn every stream into a busy loop.
	minWatchLeaseInterval = time.Second

	// watchLeaseMargin is slack added to a check's own timeout so a check
	// that is merely slow, rather than denied, never races the webhook's own
	// retry budget.
	watchLeaseMargin = time.Second

	// maxWatchBackoffSteps bounds how many backoff waits are summed one by
	// one when estimating a webhook's retry budget. From this step on every
	// wait is already clamped to MaxWaitInterval, so the rest are multiplied.
	maxWatchBackoffSteps = 63
)

type watchDeadlineKey struct{}

var errWatchLeaseExpired = errors.PermissionDenied("watch authorization lease expired")

type watchLease struct {
	failure  chan error
	cancel   context.CancelFunc
	done     chan struct{}
	mu       sync.RWMutex
	expires  time.Time
	terminal error
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
// and five seconds from this check's start.
func (l *watchLease) checkDeadline(start time.Time) time.Time {
	l.mu.RLock()
	defer l.mu.RUnlock()
	deadline := leaseExpiry(start)
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

func (l *watchLease) fail(err error) {
	l.mu.Lock()
	l.terminal = err
	l.mu.Unlock()
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

// watchLeaseInterval is the re-check period: the longest one that still
// leaves a re-check time to answer inside the lease window. It does not track
// AuthWebhookCacheTTL, because renewals do not read that cache; the cost of
// that is one webhook request per open stream per period, which is the price
// of bounding revocation by wall-clock time instead of by the TTL.
func watchLeaseInterval() time.Duration {
	return max(maxWatchLeaseAge-watchLeaseCheckAllowance, minWatchLeaseInterval)
}

// webhookBudget returns the longest a single auth webhook call may legally
// take under the project's own request timeout, retry count and backoff. It
// bounds a re-check's own timeout, so the project's configuration is never
// cut short by anything other than the lease window itself.
func webhookBudget(project *types.Project) (time.Duration, error) {
	options, err := project.GetAuthWebhookOptions()
	if err != nil {
		return 0, fmt.Errorf("watch lease budget: %w", err)
	}

	// MaxRetries is unbounded, so every step saturates rather than overflow
	// into a negative budget that would expire the lease immediately.
	budget := addDuration(
		mulDuration(options.MaxRetries, options.RequestTimeout),
		options.RequestTimeout,
	)
	steps := min(options.MaxRetries, maxWatchBackoffSteps)
	for retries := range steps {
		budget = addDuration(budget, webhook.WaitInterval(
			retries, options.MinWaitInterval, options.MaxWaitInterval,
		))
	}
	budget = addDuration(budget, mulDuration(options.MaxRetries-steps, options.MaxWaitInterval))

	return budget, nil
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

// mulDuration returns n*d for a non-negative duration, saturating at the
// largest representable duration.
func mulDuration(n uint64, d time.Duration) time.Duration {
	if n == 0 || d <= 0 {
		return 0
	}
	if n > uint64(math.MaxInt64/d) {
		return math.MaxInt64
	}
	return time.Duration(n) * d
}

// leaseExpiry bounds a stream to five seconds from the moment its
// authorization was last confirmed. A slow or uncertain webhook cannot extend
// an established stream past this age.
func leaseExpiry(confirmedAt time.Time) time.Time {
	return confirmedAt.Add(maxWatchLeaseAge)
}

// startWatchLease checks an established stream against the project's current
// authorization settings. A denial stops the stream; the write deadline
// prevents a slow send from outliving the bounded lease. The lease runs even
// when the project does not require auth today, so a project that enables or
// widens its auth webhook afterwards still cuts off streams opened before.
//
// The window runs from the caller's admission check returning, not from when
// it started: an admission that legally spent the project's whole retry budget
// has still only just confirmed authorization, and must not be rejected as
// stale by the lease that is meant to carry it.
func (s *yorkieServer) startWatchLease(
	ctx context.Context,
	access *types.AccessInfo,
) (*watchLease, error) {
	lease := &watchLease{}
	project := projects.From(ctx)
	setDeadline, _ := ctx.Value(watchDeadlineKey{}).(func(time.Time) error)
	interval := watchLeaseInterval()

	if project.RequireAuth(access.Method) {
		if setDeadline == nil {
			return nil, auth.ErrPermissionDenied
		}
		expiry := leaseExpiry(time.Now())
		if err := setDeadline(expiry); err != nil {
			return nil, err
		}
		lease.expires = expiry
	}

	leaseCtx, cancel := context.WithCancel(ctx)
	lease.cancel = cancel
	lease.failure = make(chan error, 1)
	lease.done = make(chan struct{})
	// Read on the handler goroutine: a panic here is recovered by net/http,
	// whereas one in the detached lease goroutine would take the server down.
	apiKey := metadata.From(ctx).APIKey
	go s.runWatchLease(leaseCtx, lease, access, setDeadline, interval, apiKey)

	return lease, nil
}

// runWatchLease re-checks the stream once per interval until the stream ends,
// the server stops, or a check refuses it.
func (s *yorkieServer) runWatchLease(
	leaseCtx context.Context,
	lease *watchLease,
	access *types.AccessInfo,
	setDeadline func(time.Time) error,
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

		if err := s.recheckWatchLease(leaseCtx, lease, access, setDeadline, interval, apiKey); err != nil {
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
	setDeadline func(time.Time) error,
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
		return lease.tolerate(leaseCtx, watchProjectReloadError(err), interval)
	}

	// Newly required authorization must have a finite window even if the first
	// webhook check is uncertain. Preserve an armed window until a check succeeds.
	if fresh.RequireAuth(access.Method) {
		lease.mu.RLock()
		unarmed := lease.expires.IsZero()
		lease.mu.RUnlock()
		if unarmed {
			if err := renewWatchLease(lease, setDeadline, checkStart); err != nil {
				return err
			}
		}
	}

	if err := s.checkWatchLease(checkCtx, fresh, access); err != nil {
		return lease.tolerate(leaseCtx, err, interval)
	}

	if err := lease.maySend(); err != nil {
		return err
	}
	if !fresh.RequireAuth(access.Method) {
		return releaseWatchLease(lease, setDeadline)
	}

	return renewWatchLease(lease, setDeadline, checkStart)
}

// releaseWatchLease lifts the bound from a stream whose project has stopped
// requiring authorization for this method. Without it a webhook removed or
// narrowed mid-stream would leave the admission window and its write deadline
// armed with nothing left to renew them, expiring streams that are once again
// allowed unconditionally.
func releaseWatchLease(lease *watchLease, setDeadline func(time.Time) error) error {
	if setDeadline != nil {
		if err := setDeadline(time.Time{}); err != nil {
			return err
		}
	}

	lease.mu.Lock()
	lease.expires = time.Time{}
	lease.mu.Unlock()

	return nil
}

// tolerate decides what an unconfirmed check means for the stream. A denial
// ends it immediately. Anything else — a lookup blip, a webhook outage —
// leaves current authorization unknown rather than revoked: the stream
// survives while another attempt still fits inside its window, and ends when
// no attempt does. A lease with no window, which is a project that requires no
// authorization for this method, cannot be revoked by uncertainty, so a blip
// does not drop every such stream on the node at once.
func (l *watchLease) tolerate(ctx context.Context, cause error, interval time.Duration) error {
	if errors.IsStatus(cause, errors.ErrCodePermissionDenied) ||
		errors.IsStatus(cause, errors.ErrCodeUnauthenticated) {
		return cause
	}

	l.mu.RLock()
	expires := l.expires
	l.mu.RUnlock()

	if expires.IsZero() || time.Now().Add(interval).Before(expires) {
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
// The project's own request timeout and retry policy bound the call; the lease
// window bounds it further through ctx, so a check that cannot answer inside
// the window does not renew the lease instead of running past the cutoff.
func (s *yorkieServer) checkWatchLease(
	ctx context.Context,
	project *types.Project,
	access *types.AccessInfo,
) error {
	if !project.RequireAuth(access.Method) {
		return nil
	}

	budget, err := webhookBudget(project)
	if err != nil {
		return err
	}

	checkCtx, stop := context.WithTimeout(ctx, addDuration(budget, watchLeaseMargin))
	defer stop()

	return auth.VerifyWatchLease(checkCtx, s.backend, project, access)
}

// renewWatchLease extends the stream's window after a successful check.
func renewWatchLease(
	lease *watchLease,
	setDeadline func(time.Time) error,
	checkStart time.Time,
) error {
	// A project that starts requiring auth mid-stream cannot be bounded
	// without the transport hook, so such a stream fails closed.
	if setDeadline == nil {
		return auth.ErrPermissionDenied
	}

	expiry := leaseExpiry(checkStart)
	if !time.Now().Before(expiry) {
		return errWatchLeaseExpired
	}
	if err := setDeadline(expiry); err != nil {
		return err
	}

	lease.mu.Lock()
	lease.expires = expiry
	lease.mu.Unlock()

	return nil
}
