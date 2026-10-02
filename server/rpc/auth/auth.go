/*
 * Copyright 2022 The Yorkie Authors. All rights reserved.
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

// Package auth provides authentication and authorization for RPCs.
package auth

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

// AccessAttributes returns an array of AccessAttribute from the given pack.
func AccessAttributes(pack *change.Pack) []types.AccessAttribute {
	verb := types.Read
	if pack.HasChanges() {
		verb = types.ReadWrite
	}

	// NOTE(hackerwins): In the future, methods such as bulk PushPull can be
	// added, so we declare it as an array.
	return []types.AccessAttribute{{
		Key:  pack.DocumentKey.String(),
		Verb: verb,
	}}
}

// VerifyAccess verifies the given access.
func VerifyAccess(ctx context.Context, be *backend.Backend, accessInfo *types.AccessInfo) error {
	md := metadata.From(ctx)
	prj := projects.From(ctx)

	if !prj.RequireAuth(accessInfo.Method) {
		return nil
	}

	return verifyAccess(
		ctx,
		be,
		prj,
		md.Authorization,
		accessInfo,
		false,
	)
}

// RecheckAccess verifies again an access that was granted earlier, on behalf
// of the given project and token rather than the request context's, so an
// open stream can be checked after the RPC that admitted it. It asks the
// webhook directly and once (see verifyAccess).
func RecheckAccess(
	ctx context.Context,
	be *backend.Backend,
	prj *types.Project,
	token string,
	accessInfo *types.AccessInfo,
) error {
	if !prj.RequireAuth(accessInfo.Method) {
		return nil
	}

	return verifyAccess(ctx, be, prj, token, accessInfo, true)
}

// DropCachedDecisions drops the cached auth webhook decisions of the given
// project that concern any of the keys (all of them when keys is empty), so
// the next verification of those accesses asks the webhook again.
func DropCachedDecisions(be *backend.Backend, prj *types.Project, keys []string) int {
	prefix := cacheKeyPrefix(prj.PublicKey)

	// A cache key embeds the request body, whose attributes carry each key as
	// `"key":"<key>"`; marshaling quotes it the same way the body did.
	needles := make([]string, 0, len(keys))
	for _, k := range keys {
		quoted, err := json.Marshal(k)
		if err != nil {
			continue
		}
		needles = append(needles, `"key":`+string(quoted))
	}

	return be.Cache.AuthWebhook.RemoveIf(func(cacheKey string) bool {
		if !strings.HasPrefix(cacheKey, prefix) {
			return false
		}
		if len(keys) == 0 {
			return true
		}
		for _, needle := range needles {
			if strings.Contains(cacheKey, needle) {
				return true
			}
		}
		return false
	})
}
