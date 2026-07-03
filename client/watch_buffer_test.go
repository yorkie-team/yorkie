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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWatchBuffer(t *testing.T) {
	t.Run("push never blocks and pop preserves FIFO order", func(t *testing.T) {
		buf := newWatchBuffer()
		types := []WatchDocResponseType{
			DocumentWatched, DocumentChanged, DocumentUnwatched,
		}
		for _, tp := range types {
			buf.push(WatchDocResponse{Type: tp})
		}

		ctx := context.Background()
		for _, tp := range types {
			resp, ok := buf.pop(ctx)
			assert.True(t, ok)
			assert.Equal(t, tp, resp.Type)
		}
	})

	t.Run("pop blocks until push arrives", func(t *testing.T) {
		buf := newWatchBuffer()
		done := make(chan WatchDocResponse, 1)
		go func() {
			resp, ok := buf.pop(context.Background())
			assert.True(t, ok)
			done <- resp
		}()

		buf.push(WatchDocResponse{Type: DocumentWatched})
		select {
		case resp := <-done:
			assert.Equal(t, DocumentWatched, resp.Type)
		case <-time.After(time.Second):
			t.Fatal("pop did not receive pushed item")
		}
	})

	t.Run("close drains remaining items then reports exhaustion", func(t *testing.T) {
		buf := newWatchBuffer()
		buf.push(WatchDocResponse{Type: DocumentWatched})
		buf.close()
		buf.push(WatchDocResponse{Type: DocumentChanged}) // dropped: closed

		ctx := context.Background()
		resp, ok := buf.pop(ctx)
		assert.True(t, ok)
		assert.Equal(t, DocumentWatched, resp.Type)

		_, ok = buf.pop(ctx)
		assert.False(t, ok)
	})

	t.Run("pop returns on context cancellation", func(t *testing.T) {
		buf := newWatchBuffer()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan bool, 1)
		go func() {
			_, ok := buf.pop(ctx)
			done <- ok
		}()

		cancel()
		select {
		case ok := <-done:
			assert.False(t, ok)
		case <-time.After(time.Second):
			t.Fatal("pop did not return after context cancellation")
		}
	})
}
