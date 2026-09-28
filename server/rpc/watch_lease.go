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
	// minWatchLeaseInterval floors the re-check period so a small admission
	// cache TTL cannot turn every stream into a busy loop.
	minWatchLeaseInterval = time.Second
	// maxWatchLeaseInterval leaves time for a webhook response inside the
	// five-second lease, regardless of the admission cache TTL.
	maxWatchLeaseInterval = 2 * time.Second
	// maxWatchLeaseAge bounds event delivery when a webhook or project lookup
	// cannot establish current authorization.
	maxWatchLeaseAge = 5 * time.Second

	// watchLeaseMargin is slack added to a lease window so a check that is
	// merely slow, rather than denied, never races the window it renews.
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

// leaseInterval caps the re-check period so the next webhook request starts
// soon enough to fit inside the five-second Watch cutoff.
func (s *yorkieServer) leaseInterval() time.Duration {
	return min(max(s.backend.Config.ParseAuthWebhookCacheTTL(), minWatchLeaseInterval), maxWatchLeaseInterval)
}

// webhookBudget returns the longest a single auth webhook call may legally
// take under the project's own request timeout, retry count and backoff. A
// lease window shorter than this would expire streams the webhook still
// allows, so the project's configuration bounds the lease rather than the
// other way round.
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

// leaseExpiry bounds a successful check to five seconds from its start. A
// slow or uncertain webhook cannot extend an established stream past this age.
func leaseExpiry(start time.Time) time.Time {
	return start.Add(maxWatchLeaseAge)
}

// startWatchLease checks an established stream against the project's current
// authorization settings. A denial stops the stream; the write deadline
// prevents a slow send from outliving the bounded lease. The lease runs even
// when the project does not require auth today, so a project that enables or
// widens its auth webhook afterwards still cuts off streams opened before.
func (s *yorkieServer) startWatchLease(
	ctx context.Context,
	access *types.AccessInfo,
	admissionStart time.Time,
) (*watchLease, error) {
	lease := &watchLease{}
	project := projects.From(ctx)
	setDeadline, _ := ctx.Value(watchDeadlineKey{}).(func(time.Time) error)
	interval := s.leaseInterval()

	if project.RequireAuth(access.Method) {
		if setDeadline == nil {
			return nil, auth.ErrPermissionDenied
		}
		expiry := leaseExpiry(admissionStart)
		if !time.Now().Before(expiry) {
			return nil, errWatchLeaseExpired
		}
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

		checkStart := time.Now()
		checkCtx, stop := context.WithDeadline(leaseCtx, lease.checkDeadline(checkStart))
		// The project captured when the stream opened is a snapshot. Re-read
		// it so that an auth webhook enabled or widened afterwards reaches
		// streams that are already established. If the reload fails, old
		// settings cannot establish whether authorization was just enabled.
		fresh, err := projects.GetProjectFromAPIKey(checkCtx, s.backend, apiKey)
		if err != nil {
			stop()
			if leaseCtx.Err() != nil {
				return
			}
			logging.From(leaseCtx).Warnf("watch lease: reload project: %v", err)
			lease.fail(watchProjectReloadError(err))
			return
		}

		err = s.checkWatchLease(checkCtx, fresh, access)
		stop()
		if err == nil {
			err = lease.maySend()
		}
		if err == nil && fresh.RequireAuth(access.Method) {
			err = renewWatchLease(lease, setDeadline, checkStart)
		}
		if err != nil {
			if leaseCtx.Err() != nil {
				return
			}
			lease.fail(err)
			return
		}
	}
}

// watchProjectReloadError fails closed when current project authorization
// settings cannot be established, including temporary lookup failures.
func watchProjectReloadError(err error) error {
	if stderrors.Is(err, database.ErrProjectNotFound) {
		return errors.PermissionDenied("watch project no longer exists")
	}
	return errors.PermissionDenied("watch project authorization unavailable")
}

// checkWatchLease asks the auth webhook whether the stream is still allowed.
// The configured retry policy runs within the time left after a re-check
// interval. An uncertain result fails closed before the five-second cutoff.
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

	checkCtx, stop := context.WithTimeout(ctx, min(
		addDuration(budget, watchLeaseMargin),
		maxWatchLeaseAge-s.leaseInterval(),
	))
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
