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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// VerifyWatchLease re-checks an established Watch stream against the given
// project, which the caller reloads so that settings changed after the stream
// opened take effect.
//
// Lease renewals read the webhook directly rather than the admission cache, so
// they observe current decisions even when the configured cache TTL exceeds the
// Watch cutoff. The decision is still written back to that cache, so one
// stream's observation of a revocation also denies the RPCs that read it.
//
// The cost of that directness is one webhook request per open stream per
// re-check period, which is what bounding revocation by wall-clock time rather
// than by the cache TTL requires; see watchLeaseInterval in the rpc package.
func VerifyWatchLease(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	access *types.AccessInfo,
) error {
	if !project.RequireAuth(access.Method) {
		return nil
	}

	return verifyAccessWithCache(ctx, be, project, metadata.From(ctx).Authorization, access, false)
}
