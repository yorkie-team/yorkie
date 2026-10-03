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

package client

import (
	"context"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
)

// AuthInterceptor is an interceptor for authentication.
//
// The token is swapped at runtime -- the auth-webhook flow hands the client a
// fresh one through Client.SetToken -- while the interceptors below read it
// from goroutines that never wrote it: the sync loop and every watch stream
// reader issue requests of their own. A plain string field is a two-word
// header, so an unsynchronized swap can hand such a reader a torn pointer and
// length as readily as a stale token. It is therefore stored atomically; apiKey
// is fixed at construction and needs no such treatment.
type AuthInterceptor struct {
	apiKey string
	token  atomic.Pointer[string]
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(apiKey, token string) *AuthInterceptor {
	i := &AuthInterceptor{apiKey: apiKey}
	i.token.Store(&token)
	return i
}

// SetToken sets the token.
func (i *AuthInterceptor) SetToken(token string) {
	i.token.Store(&token)
}

// loadToken returns the token currently carried by this interceptor. The zero
// value of the pointer is nil, which an interceptor built outside
// NewAuthInterceptor carries, so it reads as the empty token rather than
// panicking on the header path.
func (i *AuthInterceptor) loadToken() string {
	if token := i.token.Load(); token != nil {
		return *token
	}
	return ""
}

// WrapUnary creates a unary server interceptor for authorization.
func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		req.Header().Add(types.APIKeyKey, i.apiKey)
		req.Header().Add(types.AuthorizationKey, i.loadToken())
		req.Header().Add(types.UserAgentKey, types.GoSDKType+"/"+version.Version)

		return next(ctx, req)
	}
}

// WrapStreamingClient creates a stream client interceptor for authorization.
func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		conn.RequestHeader().Set(types.APIKeyKey, i.apiKey)
		conn.RequestHeader().Set(types.AuthorizationKey, i.loadToken())
		conn.RequestHeader().Set(types.UserAgentKey, types.GoSDKType+"/"+version.Version)
		return conn
	}
}

// WrapStreamingHandler creates a stream server interceptor for authorization.
func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		return next(ctx, conn)
	}
}
