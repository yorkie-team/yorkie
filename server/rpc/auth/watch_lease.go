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

package auth

import (
	"context"
	"fmt"

	"golang.org/x/sync/singleflight"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// leaseChecks coalesces re-checks that would ask the webhook the same
// question at the same time. Without it a client could multiply its load on
// the webhook by opening more streams; with it, streams sharing a token,
// method and resource set cost one request per re-check period however many
// of them are open.
var leaseChecks singleflight.Group

// VerifyWatchLease re-checks an established Watch stream against the given
// project, which the caller reloads so that settings changed after the stream
// opened take effect.
//
// Lease renewals read the webhook directly rather than the admission cache, so
// they observe current decisions even when the configured cache TTL exceeds the
// Watch cutoff. The decision is still written back to that cache, so one
// stream's observation of a revocation also denies the RPCs that read it.
//
// Directness costs one webhook request per re-check period, which is what
// bounding revocation by wall-clock time rather than by the cache TTL
// requires; identical checks are coalesced so that cost is per question asked
// rather than per stream open. See watchLeaseInterval in the rpc package.
func VerifyWatchLease(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	access *types.AccessInfo,
) error {
	if !project.RequireAuth(access.Method) {
		return nil
	}

	token := metadata.From(ctx).Authorization
	key := fmt.Sprintf("%s:lease:%s:%s:%v", project.PublicKey, token, access.Method, access.Attributes)

	// The shared call outlives the cancellation of whichever stream happens to
	// make it, so one stream ending does not fail the checks waiting on it. It
	// stays bounded by that stream's deadline, which the lease window sets.
	callCtx := context.WithoutCancel(ctx)
	if deadline, ok := ctx.Deadline(); ok {
		var stop context.CancelFunc
		callCtx, stop = context.WithDeadline(callCtx, deadline)
		defer stop()
	}

	_, err, _ := leaseChecks.Do(key, func() (any, error) {
		return nil, verifyAccessWithCache(callCtx, be, project, token, access, false)
	})
	if err != nil {
		return fmt.Errorf("verify watch lease: %w", err)
	}

	return nil
}
