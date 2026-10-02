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
	"sync/atomic"
	"testing"

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

func TestWatchRegistry(t *testing.T) {
	ctx := logging.With(context.Background(), logging.DefaultLogger())
	const projectA, projectB = types.ID("project-a"), types.ID("project-b")

	t.Run("closes only covered streams of the project", func(t *testing.T) {
		r := newWatchRegistry()
		covered, release1 := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1", "room-1"))
		defer release1()
		otherKey, release2 := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-2"))
		defer release2()
		otherProject, release3 := r.register(ctx, projectB, "alice", watchAccess(types.Watch, "room-1"))
		defer release3()

		closed, err := r.revalidate(ctx, projectA, []string{"room-1"}, func(
			context.Context, string, *types.AccessInfo,
		) error {
			return auth.ErrPermissionDenied
		})

		assert.NoError(t, err)
		assert.Equal(t, 1, closed)
		assert.ErrorIs(t, context.Cause(covered), auth.ErrPermissionDenied)
		assert.NoError(t, otherKey.Err())
		assert.NoError(t, otherProject.Err())
	})

	t.Run("empty keys cover every stream of the project", func(t *testing.T) {
		r := newWatchRegistry()
		s1, release1 := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer release1()
		s2, release2 := r.register(ctx, projectA, "bob", watchAccess(types.WatchChannel, "room-1"))
		defer release2()

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
		s1, release1 := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer release1()
		s2, release2 := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer release2()
		s3, release3 := r.register(ctx, projectA, "bob", watchAccess(types.Watch, "doc-1"))
		defer release3()

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

	t.Run("a failure to verify closes as retryable, not as a denial", func(t *testing.T) {
		r := newWatchRegistry()
		s, release := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer release()

		closed, err := r.revalidate(ctx, projectA, nil, func(
			context.Context, string, *types.AccessInfo,
		) error {
			return errors.New("webhook timed out")
		})

		assert.NoError(t, err)
		assert.Equal(t, 1, closed)
		assert.ErrorIs(t, context.Cause(s), ErrRevalidationUnavailable)
	})

	t.Run("release removes the stream", func(t *testing.T) {
		r := newWatchRegistry()
		s, release := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		assert.Equal(t, 1, r.len())

		release()

		assert.Equal(t, 0, r.len())
		assert.ErrorIs(t, context.Cause(s), context.Canceled)
		closed, err := r.revalidate(ctx, projectA, nil, func(
			context.Context, string, *types.AccessInfo,
		) error {
			return auth.ErrPermissionDenied
		})
		assert.NoError(t, err)
		assert.Equal(t, 0, closed)
	})

	t.Run("an ended revalidation closes nothing it did not verify", func(t *testing.T) {
		r := newWatchRegistry()
		s, release := r.register(ctx, projectA, "alice", watchAccess(types.Watch, "doc-1"))
		defer release()

		revalidateCtx, cancel := context.WithCancel(ctx)
		closed, err := r.revalidate(revalidateCtx, projectA, nil, func(
			ctx context.Context, _ string, _ *types.AccessInfo,
		) error {
			// The webhook call is still in flight when the caller gives up.
			cancel()
			<-ctx.Done()
			return ctx.Err()
		})

		assert.ErrorIs(t, err, context.Canceled)
		assert.Equal(t, 0, closed)
		assert.NoError(t, s.Err())
	})

	t.Run("keys match exactly", func(t *testing.T) {
		r := newWatchRegistry()
		sub, release := r.register(ctx, projectA, "alice", watchAccess(types.WatchChannel, "room-1.sub"))
		defer release()

		closed, err := r.revalidate(ctx, projectA, []string{"room-1"}, func(
			context.Context, string, *types.AccessInfo,
		) error {
			return auth.ErrPermissionDenied
		})

		assert.NoError(t, err)
		assert.Equal(t, 0, closed)
		assert.NoError(t, sub.Err())
	})
}
