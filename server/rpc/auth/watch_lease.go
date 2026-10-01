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

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// VerifyWatchLease re-checks an established Watch stream against the given
// project, which the caller reloads so that settings changed after the stream
// opened take effect.
//
// A cached denial answers the check on the spot, so a revoked stream costs the
// webhook nothing; a cached allow does not, because the whole point of the
// lease is to notice a revocation the cached allow still hides. The re-check
// period is the operator's own AuthWebhookCacheTTL (see watchLeaseInterval in
// the rpc package), so one stream asks the webhook no more often than that
// cache would have let it. The decision is written back to the cache, so one
// stream's observation of a revocation also denies the RPCs that read it.
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
	if err := verifyAccessWithCache(ctx, be, project, token, access, cacheDenialsOnly); err != nil {
		return fmt.Errorf("verify watch lease: %w", err)
	}

	return nil
}
