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

package users

import (
	"context"

	"github.com/yorkie-team/yorkie/api/types"
)

// userKey is the key for the context.Context.
type userKey struct{}

// From returns the user from the context.
func From(ctx context.Context) *types.User {
	return ctx.Value(userKey{}).(*types.User)
}

// With returns a new context with the given user.
func With(ctx context.Context, user *types.User) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}
