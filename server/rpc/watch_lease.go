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
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

const (
	watchLeaseInterval = time.Second
	watchCheckTimeout  = time.Second
	watchWriteLifetime = 4 * time.Second
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

func (l *watchLease) stop() {
	if l.cancel != nil {
		l.cancel()
		<-l.done
	}
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
		deadline := func(at time.Time) error {
			return http.NewResponseController(w).SetWriteDeadline(at)
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), watchDeadlineKey{}, deadline)))
	})
}

// startWatchLease checks an established stream independently of the ordinary
// authorization cache. A failed or uncertain check stops the stream; the
// write deadline prevents a slow send from outliving the bounded lease.
func (s *yorkieServer) startWatchLease(
	ctx context.Context,
	access *types.AccessInfo,
	admissionStart time.Time,
) (*watchLease, error) {
	lease := &watchLease{}
	if !projects.From(ctx).RequireAuth(access.Method) {
		return lease, nil
	}
	deadline, ok := ctx.Value(watchDeadlineKey{}).(func(time.Time) error)
	if !ok {
		return nil, auth.ErrPermissionDenied
	}
	initialExpiry := admissionStart.Add(watchWriteLifetime)
	if !time.Now().Before(initialExpiry) {
		return nil, errWatchLeaseExpired
	}
	if err := deadline(initialExpiry); err != nil {
		return nil, err
	}
	lease.expires = initialExpiry

	leaseCtx, cancel := context.WithCancel(ctx)
	lease.cancel = cancel
	lease.failure = make(chan error, 1)
	lease.done = make(chan struct{})
	go func() {
		defer close(lease.done)
		ticker := time.NewTicker(watchLeaseInterval)
		defer ticker.Stop()
		for {
			select {
			case <-leaseCtx.Done():
				return
			case <-s.serviceCtx.Done():
				return
			case <-ticker.C:
				checkStart := time.Now()
				checkCtx, stop := context.WithTimeout(leaseCtx, watchCheckTimeout)
				err := auth.VerifyWatchLease(checkCtx, s.backend, access)
				stop()
				if err == nil {
					err = lease.maySend()
				}
				if err == nil {
					newExpiry := checkStart.Add(watchWriteLifetime)
					if !time.Now().Before(newExpiry) {
						err = errWatchLeaseExpired
					} else {
						err = deadline(newExpiry)
						if err == nil {
							lease.mu.Lock()
							lease.expires = newExpiry
							lease.mu.Unlock()
						}
					}
				}
				if err != nil {
					lease.mu.Lock()
					lease.terminal = err
					lease.mu.Unlock()
					lease.failure <- err
					return
				}
			}
		}
	}()
	return lease, nil
}
