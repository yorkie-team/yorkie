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
	"sync"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/pkg/document/change"
	pkgtypes "github.com/yorkie-team/yorkie/pkg/types"
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

// cacheGen counts the drops of cached decisions. A verification reads it
// before asking the webhook and writes the answer back only while it still
// matches, so an answer the webhook gave before a drop is never cached after
// it. Without this, a verification already in flight when a revocation lands
// re-caches the decision the revocation dropped, and the next access reads
// the revoked decision from the cache.
//
// The lock makes the drop and the write-back exclusive of each other: a bare
// counter would still allow a write that read the generation, found it
// unchanged, and only then raced the drop's removal.
var (
	cacheGenMu sync.RWMutex
	cacheGen   uint64
)

// currentCacheGen returns the generation a webhook answer must still see to
// be worth caching.
func currentCacheGen() uint64 {
	cacheGenMu.RLock()
	defer cacheGenMu.RUnlock()
	return cacheGen
}

// cacheDecision caches the webhook answer unless the decisions were dropped
// since gen was read, in which case the answer predates the drop and caching
// it would undo the drop.
func cacheDecision(
	be *backend.Backend,
	cacheKey string,
	gen uint64,
	entry pkgtypes.Pair[int, *types.AuthWebhookResponse],
) {
	cacheGenMu.RLock()
	defer cacheGenMu.RUnlock()

	if cacheGen != gen {
		return
	}
	be.Cache.AuthWebhook.Add(cacheKey, entry)
}

// DropCachedDecisions drops the cached auth webhook decisions of the given
// project that concern any of the keys (all of them when keys is empty), so
// the next verification of those accesses asks the webhook again. Answers
// that were already in flight are dropped too: they predate this call, so
// they are not written back (see cacheGen). With the cache disabled nothing is
// ever cached, so there is nothing to drop.
func DropCachedDecisions(be *backend.Backend, prj *types.Project, keys []string) int {
	if be.Config.AuthWebhookCacheDisabled {
		return 0
	}

	cacheGenMu.Lock()
	defer cacheGenMu.Unlock()
	cacheGen++

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
