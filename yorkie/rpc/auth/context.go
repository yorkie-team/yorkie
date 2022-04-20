/*
 * Copyright 2021 The Yorkie Authors. All rights reserved.
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

import "context"

// key is the key for the context.Context.
type key int

// tokenKey is the key for the token.
const tokenKey key = 0

// clientAPIKeyKey is the key for the client API key.
const clientAPIKeyKey key = 1

// TokenFromCtx returns the token from the given context.
func TokenFromCtx(ctx context.Context) string {
	return ctx.Value(tokenKey).(string)
}

// CtxWithToken creates a new context with the given token.
func CtxWithToken(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, tokenKey, token)
}

// ClientAPIKeyFromCtx returns the clientAPIKey from the given context.
func ClientAPIKeyFromCtx(ctx context.Context) string {
	return ctx.Value(clientAPIKeyKey).(string)
}

// CtxWithClientAPIKey creates a new context with the given clientAPIKey.
func CtxWithClientAPIKey(ctx context.Context, clientAPIKey string) context.Context {
	return context.WithValue(ctx, clientAPIKeyKey, clientAPIKey)
}
