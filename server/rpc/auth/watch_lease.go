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
// It shares the admission cache on purpose. AuthWebhookCacheTTL is the
// configured bound on how stale an authorization decision may be, and the
// caller re-checks on that same period, so N streams of one project cost at
// most one webhook call per TTL rather than one call per stream per re-check.
// The price is revocation latency: an allow cached just before a revoke can
// satisfy one more re-check, so a stream closes within about two TTLs plus
// the webhook budget. A deployment that needs a tighter cutoff lowers the TTL.
func VerifyWatchLease(
	ctx context.Context,
	be *backend.Backend,
	project *types.Project,
	access *types.AccessInfo,
) error {
	if !project.RequireAuth(access.Method) {
		return nil
	}

	return verifyAccess(ctx, be, project, metadata.From(ctx).Authorization, access)
}
