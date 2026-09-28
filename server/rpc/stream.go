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
	"reflect"

	"github.com/yorkie-team/yorkie/server/backend/pubsub"
)

// isSkipped reports whether convert asked for the event to be skipped by
// returning a nil response.
//
// Resp is instantiated with a pointer type (*api.WatchChannelResponse and
// friends), so a nil response is a typed nil pointer: converting it to an
// interface yields a non-nil interface value, and comparing `any(resp)` to
// nil would never match. The nil has to be read off the value itself.
func isSkipped[Resp any](resp Resp) bool {
	v := reflect.ValueOf(&resp).Elem()
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// streamEvents reads events from a subscription and sends converted responses
// over a stream. It blocks until the context is done, the serviceCtx is done
// (if non-nil), or the subscription channel is closed.
//
// A closed subscription channel means the subscription pruned itself (see
// pubsub.Subscription.Publish), so the stream ends with ErrSubscriptionsClosed
// rather than cleanly: the client asked for a watch that is still supposed to
// be running, and a clean end would leave it waiting on a stream no SDK
// reconnects.
//
// The convert function transforms an event into a response. If it returns
// (nil, nil), the event is skipped. The optional afterSend callback is called
// after each successful send.
//
// Authorization is not re-evaluated here. The caller verifies access once,
// before subscribing, and this loop runs until the client or the server ends
// it, so a client whose authorization is revoked mid-stream keeps receiving
// events until it reconnects. Disabling the auth webhook response cache does
// not close that window — it only applies to requests that reach the webhook,
// and an open stream makes none. Bounding it needs a re-authorization tick in
// this loop, which is deliberately out of scope for this change.
func streamEvents[E any, Resp any](
	ctx context.Context,
	serviceCtx context.Context,
	sub *pubsub.Subscription[E],
	send func(Resp) error,
	convert func(E) (Resp, error),
	afterSend func(E),
) error {
	for {
		select {
		case <-serviceCtx.Done():
			return context.Canceled
		case <-ctx.Done():
			return context.Canceled
		case event, ok := <-sub.Events():
			if !ok {
				return ErrSubscriptionsClosed
			}

			resp, err := convert(event)
			if err != nil {
				return err
			}

			// A nil response means skip this event.
			if isSkipped(resp) {
				continue
			}

			if err := send(resp); err != nil {
				return err
			}

			if afterSend != nil {
				afterSend(event)
			}
		}
	}
}
