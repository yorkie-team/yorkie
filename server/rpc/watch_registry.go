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
	goerrors "errors"
	"fmt"
	"sync"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// revalidateConcurrency bounds how many webhook calls one revalidation makes
// at the same time.
const revalidateConcurrency = 16

// ErrRevalidationUnavailable closes a stream whose authorization could not be
// confirmed during a revalidation. It is retryable: the client reconnects and
// the new stream goes through admission again.
var ErrRevalidationUnavailable = errors.Unavailable(
	"authorization could not be revalidated",
).WithCode("ErrRevalidationUnavailable")

// watchVerifier verifies one access of an open stream.
type watchVerifier func(ctx context.Context, token string, access *types.AccessInfo) error

// watchStream is an admitted Watch stream as the registry sees it: what it
// was authorized for, and how to end it.
type watchStream struct {
	projectID types.ID
	token     string
	access    types.AccessInfo
	ctx       context.Context
	close     context.CancelCauseFunc
}

// covers reports whether the stream watches any of the given keys. Keys match
// exactly: a channel key does not cover its sub-paths. A nil key set covers
// every stream.
func (w *watchStream) covers(keys map[string]struct{}) bool {
	if keys == nil {
		return true
	}
	for _, attr := range w.access.Attributes {
		if _, ok := keys[attr.Key]; ok {
			return true
		}
	}
	return false
}

// decisionKey identifies what the webhook is asked about for this stream, so
// streams asking the same question share one webhook call.
func (w *watchStream) decisionKey() string {
	return fmt.Sprintf("%s\x00%s\x00%v", w.token, w.access.Method, w.access.Attributes)
}

// watchRegistry tracks the Watch streams this node serves, so their
// authorization can be checked again after admission. Admission alone
// authorizes a stream for as long as it stays open; the registry is how a
// revocation reaches streams that are already open.
type watchRegistry struct {
	mu      sync.Mutex
	streams map[*watchStream]struct{}
}

// newWatchRegistry creates an empty registry.
func newWatchRegistry() *watchRegistry {
	return &watchRegistry{streams: make(map[*watchStream]struct{})}
}

// register adds a stream and returns the context it must stream under. The
// context ends with the revalidation error when a revalidation closes the
// stream. release removes the stream and must be called when it ends.
func (r *watchRegistry) register(
	ctx context.Context,
	projectID types.ID,
	token string,
	access types.AccessInfo,
) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(ctx)
	stream := &watchStream{
		projectID: projectID,
		token:     token,
		access:    access,
		ctx:       ctx,
		close:     cancel,
	}

	r.mu.Lock()
	r.streams[stream] = struct{}{}
	r.mu.Unlock()

	return ctx, func() {
		r.mu.Lock()
		delete(r.streams, stream)
		r.mu.Unlock()
		cancel(nil)
	}
}

// len returns the number of registered streams.
func (r *watchRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// revalidate verifies again every stream of the project that covers any of
// the keys, and closes the ones that no longer pass. Streams asking the same
// question share one verification. It returns the number of closed streams.
//
// A denial closes the stream with the denial itself. A failure of the webhook
// closes it with ErrRevalidationUnavailable: the server cannot tell whether
// access is still granted, and a reconnect resolves that through admission
// rather than by a policy of its own. A failure of the revalidation itself
// (its context ending) closes nothing, since it says nothing about the
// access; the streams it did not reach are reported as an error so the
// caller retries.
func (r *watchRegistry) revalidate(
	ctx context.Context,
	projectID types.ID,
	keys []string,
	verify watchVerifier,
) (int, error) {
	var keySet map[string]struct{}
	if len(keys) > 0 {
		keySet = make(map[string]struct{}, len(keys))
		for _, k := range keys {
			keySet[k] = struct{}{}
		}
	}

	groups := make(map[string][]*watchStream)
	r.mu.Lock()
	for stream := range r.streams {
		if stream.projectID == projectID && stream.covers(keySet) {
			key := stream.decisionKey()
			groups[key] = append(groups[key], stream)
		}
	}
	r.mu.Unlock()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		closed     int
		unverified int
		sem        = make(chan struct{}, revalidateConcurrency)
	)
	for _, streams := range groups {
		wg.Go(func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
			}

			err := ctx.Err()
			if err == nil {
				err = verify(ctx, streams[0].token, &streams[0].access)
			}
			if err == nil {
				return
			}
			if ctx.Err() != nil {
				mu.Lock()
				unverified += len(streams)
				mu.Unlock()
				return
			}
			if !isDenial(err) {
				// The cause stays in the log: it can name the webhook, which
				// is not the client's to see.
				logging.From(ctx).Warnf("revalidate watch access: %v", err)
				err = ErrRevalidationUnavailable
			}
			n := 0
			for _, stream := range streams {
				// A stream that already ended is not one this call closed.
				if stream.ctx.Err() == nil {
					n++
				}
				stream.close(err)
			}

			mu.Lock()
			closed += n
			mu.Unlock()
		})
	}
	wg.Wait()

	if unverified > 0 {
		return closed, fmt.Errorf(
			"revalidate watch access: %d streams left unverified: %w", unverified, ctx.Err(),
		)
	}
	return closed, nil
}

// isDenial reports whether the error is the webhook refusing the access, as
// opposed to failing to answer.
func isDenial(err error) bool {
	return goerrors.Is(err, auth.ErrPermissionDenied) || goerrors.Is(err, auth.ErrUnauthenticated)
}
