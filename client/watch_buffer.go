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

package client

import (
	"context"
	"sync"
)

// watchBuffer is an unbounded FIFO queue between watch event producers and
// the single goroutine that delivers responses to the user-facing watch
// response channel. Push never blocks, so producers holding the document
// mutex are never coupled to how fast the application consumes responses.
type watchBuffer struct {
	mu     sync.Mutex
	items  []WatchDocResponse
	closed bool

	// signal wakes a waiting pop; capacity 1 so push never blocks.
	signal chan struct{}
}

// newWatchBuffer creates a new watchBuffer.
func newWatchBuffer() *watchBuffer {
	return &watchBuffer{
		signal: make(chan struct{}, 1),
	}
}

// push appends the given response to the buffer. It never blocks. Pushing
// to a closed buffer is a no-op.
func (b *watchBuffer) push(resp WatchDocResponse) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return
	}
	b.items = append(b.items, resp)
	b.mu.Unlock()

	select {
	case b.signal <- struct{}{}:
	default:
	}
}

// close marks the buffer as closed. Items already buffered can still be
// drained by pop; once drained, pop returns false.
func (b *watchBuffer) close() {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()

	select {
	case b.signal <- struct{}{}:
	default:
	}
}

// pop removes and returns the oldest response. It blocks until an item is
// available, the buffer is closed and drained, or the context is done. The
// second return value is false when there is nothing more to deliver.
func (b *watchBuffer) pop(ctx context.Context) (WatchDocResponse, bool) {
	for {
		b.mu.Lock()
		if len(b.items) > 0 {
			resp := b.items[0]
			b.items = b.items[1:]
			b.mu.Unlock()
			return resp, true
		}
		if b.closed {
			b.mu.Unlock()
			return WatchDocResponse{}, false
		}
		b.mu.Unlock()

		select {
		case <-b.signal:
		case <-ctx.Done():
			return WatchDocResponse{}, false
		}
	}
}
