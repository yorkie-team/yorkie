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
	"encoding/json"
	goerrors "errors"
	"fmt"
	"sync"
	"sync/atomic"
	gotime "time"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/errors"
	"github.com/yorkie-team/yorkie/pkg/webhook"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

// revalidateConcurrency bounds how many webhook calls one revalidation makes
// at the same time.
const revalidateConcurrency = 16

// revalidateReplyMargin is the part of the caller's deadline a revalidation
// keeps to answer with. A node serving many distinct accesses can have more
// webhook calls to make than the deadline allows; stopping early reports what
// was verified as an error the caller can retry, instead of letting the
// deadline kill the call with no answer at all.
const revalidateReplyMargin = 500 * gotime.Millisecond

// ErrRevalidationIncomplete is returned when a revalidation could not get a
// definite answer for some streams. Those streams are left as they were, and
// the whole revalidation can be retried.
var ErrRevalidationIncomplete = errors.Unavailable(
	"some streams could not be revalidated",
).WithCode("ErrRevalidationIncomplete")

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

	// admitted is set once the stream passed admission. A stream a
	// revalidation closes before that is closed all the same, but is not
	// counted: it was never served.
	admitted atomic.Bool
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
// streams asking the same question share one webhook call. The attributes
// are keyed by their JSON, as the webhook receives them: %v would print a
// pointer field such as PresenceOnly as an address, and streams asking the
// same question would stop sharing a call.
func (w *watchStream) decisionKey() string {
	attrs, err := json.Marshal(w.access.Attributes)
	if err != nil {
		attrs = fmt.Appendf(nil, "%v", w.access.Attributes)
	}
	return w.token + "\x00" + string(w.access.Method) + "\x00" + string(attrs)
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

// register adds a stream and returns the context it must stream under,
// which ends with the denial when a revalidation closes the stream. The
// caller marks the stream admitted once admission passed, and releases it
// when it ends.
func (r *watchRegistry) register(
	ctx context.Context,
	projectID types.ID,
	token string,
	access types.AccessInfo,
) (context.Context, *watchStream) {
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

	return ctx, stream
}

// release removes the stream and ends its context.
func (r *watchRegistry) release(stream *watchStream) {
	r.mu.Lock()
	delete(r.streams, stream)
	r.mu.Unlock()
	stream.close(nil)
}

// len returns the number of registered streams.
func (r *watchRegistry) len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.streams)
}

// revalidate verifies again every stream of the project that covers any of
// the keys, and closes the ones that are denied. Streams asking the same
// question share one verification. It returns the number of closed streams.
//
// Only a definite answer changes a stream: an allow keeps it, a denial
// closes it with the denial. A denial is whatever admission would reject on,
// including an answer that does not conform (see isDenial). Not having an
// answer at all (the webhook unreachable or timing out, the revalidation's
// own context ending) says nothing about the access, so the stream is left as
// it was and the revalidation reports ErrRevalidationIncomplete for the
// caller to retry. Closing on a missing answer would disconnect users who
// kept access and send them all to a failing webhook at once.
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

	// Webhook calls stop before the caller's deadline, so the groups that did
	// get an answer are reported rather than lost with the connection.
	if deadline, ok := ctx.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-revalidateReplyMargin))
		defer cancel()
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

	// Once the context ends, revalidateGroup returns at once, so the rest of
	// the queue drains as unverified instead of being dropped uncounted.
	queue := make(chan []*watchStream)
	go func() {
		defer close(queue)
		for _, streams := range groups {
			queue <- streams
		}
	}()

	var (
		wg         sync.WaitGroup
		mu         sync.Mutex
		closed     int
		unverified int
		firstErr   error
	)
	for range min(revalidateConcurrency, len(groups)) {
		wg.Go(func() {
			for streams := range queue {
				n, err := revalidateGroup(ctx, streams, verify)

				mu.Lock()
				closed += n
				if err != nil {
					unverified += len(streams)
					if firstErr == nil {
						firstErr = err
					}
				}
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	if unverified > 0 {
		// The cause stays in the log: it can name the webhook.
		logging.From(ctx).Warnf(
			"revalidate watch access: %d streams left unverified, first error: %v",
			unverified, firstErr,
		)
		return closed, fmt.Errorf("%d streams left unverified: %w", unverified, ErrRevalidationIncomplete)
	}
	return closed, nil
}

// revalidateGroup verifies once for streams asking the same question and
// closes them on a denial. It returns how many admitted streams it closed,
// or the error when the answer was not definite.
func revalidateGroup(ctx context.Context, streams []*watchStream, verify watchVerifier) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}

	err := verify(ctx, streams[0].token, &streams[0].access)
	if err == nil {
		return 0, nil
	}
	if !isDenial(err) {
		return 0, err
	}

	closed := 0
	for _, stream := range streams {
		// A stream that already ended, or was never served, is not one this
		// call closed.
		if stream.ctx.Err() == nil && stream.admitted.Load() {
			closed++
		}
		stream.close(err)
	}
	return closed, nil
}

// isDenial reports whether the error is the webhook refusing the access, as
// opposed to failing to answer.
//
// An answer that does not conform — 200 with allowed=false, an unexpected
// status, a body that does not parse — is a refusal here, because it is one
// at admission: a Watch that gets such an answer is rejected. Were
// revalidation to call it uncertain, a webhook could keep a revoked stream
// open forever by answering in a shape neither side accepts, however often
// the revocation is retried. Closing on it is also the recoverable side of
// the choice: the client reconnects, and admission then asks again under the
// project's full retry policy.
func isDenial(err error) bool {
	return goerrors.Is(err, auth.ErrPermissionDenied) ||
		goerrors.Is(err, auth.ErrUnauthenticated) ||
		goerrors.Is(err, webhook.ErrInvalidJSONResponse) ||
		goerrors.Is(err, webhook.ErrUnexpectedStatusCode)
}
