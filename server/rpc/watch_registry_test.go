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
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	gotime "time"

	"github.com/stretchr/testify/assert"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
)

func watchAccess(method types.Method, keys ...string) types.AccessInfo {
	attrs := make([]types.AccessAttribute, len(keys))
	for i, k := range keys {
		attrs[i] = types.AccessAttribute{Key: k, Verb: types.Read}
	}
	return types.AccessInfo{Method: method, Attributes: attrs}
}

// admit registers an admitted stream and releases it when the test ends.
func admit(
	t *testing.T,
	r *watchRegistry,
	projectID types.ID,
	token string,
	access types.AccessInfo,
) context.Context {
	ctx, stream := r.register(
		logging.With(context.Background(), logging.DefaultLogger()), projectID, token, access,
	)
	stream.admitted.Store(true)
	t.Cleanup(func() { r.release(stream) })
	return ctx
}

func deny(context.Context, string, *types.AccessInfo) error { return auth.ErrPermissionDenied }

func TestWatchRegistry(t *testing.T) {
	ctx := logging.With(context.Background(), logging.DefaultLogger())
	const projectA, projectB = types.ID("project-a"), types.ID("project-b")

	t.Run("closes only covered streams of the project", func(t *testing.T) {
		r := newWatchRegistry()
		covered := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-1", "room-1"))
		otherKey := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-2"))
		otherProject := admit(t, r, projectB, "alice", watchAccess(types.Watch, "room-1"))

		closed, err := r.revalidate(ctx, projectA, []string{"room-1"}, deny)

		assert.NoError(t, err)
		assert.Equal(t, 1, closed)
		assert.ErrorIs(t, context.Cause(covered), auth.ErrPermissionDenied)
		assert.NoError(t, otherKey.Err())
		assert.NoError(t, otherProject.Err())
	})

	t.Run("keys match exactly", func(t *testing.T) {
		r := newWatchRegistry()
		sub := admit(t, r, projectA, "alice", watchAccess(types.WatchChannel, "room-1.sub"))

		closed, err := r.revalidate(ctx, projectA, []string{"room-1"}, deny)

		assert.NoError(t, err)
		assert.Equal(t, 0, closed)
		assert.NoError(t, sub.Err())
	})

	t.Run("empty keys cover every stream of the project", func(t *testing.T) {
		r := newWatchRegistry()
		s1 := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		s2 := admit(t, r, projectA, "bob", watchAccess(types.WatchChannel, "room-1"))

		closed, err := r.revalidate(ctx, projectA, nil, func(
			context.Context, string, *types.AccessInfo,
		) error {
			return auth.ErrUnauthenticated
		})

		assert.NoError(t, err)
		assert.Equal(t, 2, closed)
		assert.ErrorIs(t, context.Cause(s1), auth.ErrUnauthenticated)
		assert.ErrorIs(t, context.Cause(s2), auth.ErrUnauthenticated)
	})

	t.Run("streams asking the same question share one verification", func(t *testing.T) {
		r := newWatchRegistry()
		s1 := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		s2 := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		s3 := admit(t, r, projectA, "bob", watchAccess(types.Watch, "doc-1"))

		var calls atomic.Int32
		closed, err := r.revalidate(ctx, projectA, nil, func(
			_ context.Context, token string, _ *types.AccessInfo,
		) error {
			calls.Add(1)
			if token == "alice" {
				return auth.ErrPermissionDenied
			}
			return nil
		})

		assert.NoError(t, err)
		assert.Equal(t, int32(2), calls.Load())
		assert.Equal(t, 2, closed)
		assert.Error(t, s1.Err())
		assert.Error(t, s2.Err())
		assert.NoError(t, s3.Err())
	})

	t.Run("an uncertain answer leaves streams open and reports it", func(t *testing.T) {
		r := newWatchRegistry()
		failing := admit(t, r, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		denied := admit(t, r, projectA, "bob", watchAccess(types.Watch, "doc-1"))

		closed, err := r.revalidate(ctx, projectA, nil, func(
			_ context.Context, token string, _ *types.AccessInfo,
		) error {
			if token == "alice" {
				return errors.New("webhook returned 503")
			}
			return auth.ErrPermissionDenied
		})

		assert.ErrorIs(t, err, ErrRevalidationIncomplete)
		assert.Equal(t, 1, closed)
		assert.NoError(t, failing.Err())
		assert.ErrorIs(t, context.Cause(denied), auth.ErrPermissionDenied)
	})

	t.Run("an ended revalidation closes nothing it did not verify", func(t *testing.T) {
		r := newWatchRegistry()
		streams := make([]context.Context, 0, 3*revalidateConcurrency)
		for i := range cap(streams) {
			streams = append(streams, admit(t, r, projectA, fmt.Sprintf("user-%d", i),
				watchAccess(types.Watch, "doc-1")))
		}

		revalidateCtx, cancel := context.WithCancel(ctx)
		closed, err := r.revalidate(revalidateCtx, projectA, nil, func(
			ctx context.Context, _ string, _ *types.AccessInfo,
		) error {
			// Webhook calls are still in flight when the caller gives up.
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})

		assert.ErrorIs(t, err, ErrRevalidationIncomplete)
		assert.Contains(t, err.Error(), fmt.Sprintf("%d streams", len(streams)))
		assert.Equal(t, 0, closed)
		for _, s := range streams {
			assert.NoError(t, s.Err())
		}
	})

	t.Run("at most revalidateConcurrency verifications run at once", func(t *testing.T) {
		r := newWatchRegistry()
		for i := range 4 * revalidateConcurrency {
			admit(t, r, projectA, fmt.Sprintf("user-%d", i), watchAccess(types.Watch, "doc-1"))
		}

		var running, peak atomic.Int32
		_, err := r.revalidate(ctx, projectA, nil, func(
			context.Context, string, *types.AccessInfo,
		) error {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			gotime.Sleep(gotime.Millisecond)
			running.Add(-1)
			return nil
		})

		assert.NoError(t, err)
		assert.LessOrEqual(t, peak.Load(), int32(revalidateConcurrency))
	})

	t.Run("a stream closed before admission is not counted", func(t *testing.T) {
		r := newWatchRegistry()
		pending, stream := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer r.release(stream)

		closed, err := r.revalidate(ctx, projectA, nil, deny)

		assert.NoError(t, err)
		assert.Equal(t, 0, closed)
		assert.ErrorIs(t, context.Cause(pending), auth.ErrPermissionDenied)
	})

	t.Run("release removes the stream", func(t *testing.T) {
		r := newWatchRegistry()
		s, stream := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		assert.Equal(t, 1, r.len())

		r.release(stream)

		assert.Equal(t, 0, r.len())
		assert.ErrorIs(t, context.Cause(s), context.Canceled)
		closed, err := r.revalidate(ctx, projectA, nil, deny)
		assert.NoError(t, err)
		assert.Equal(t, 0, closed)
	})
}
