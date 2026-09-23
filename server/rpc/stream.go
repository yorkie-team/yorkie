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

	"github.com/yorkie-team/yorkie/server/backend/pubsub"
)

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

			// A nil interface value means skip this event.
			if any(resp) == nil {
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
