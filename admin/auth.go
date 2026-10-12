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

package admin

import (
	"context"
	"fmt"
	"sync/atomic"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
	"github.com/yorkie-team/yorkie/server/projects"
)

// AuthInterceptor is an interceptor for authentication.
//
// The token is stored atomically for the same reason as in the client
// package's AuthInterceptor: SetToken swaps it at runtime -- LogIn hands the
// client a fresh one -- while requests issued from other goroutines read it,
// and an unsynchronized swap of a string can hand a reader a torn pointer and
// length.
type AuthInterceptor struct {
	token atomic.Pointer[string]
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(token string) *AuthInterceptor {
	i := &AuthInterceptor{}
	i.token.Store(&token)
	return i
}

// SetToken sets the token of the client.
func (i *AuthInterceptor) SetToken(token string) {
	i.token.Store(&token)
}

// loadToken returns the token currently carried by this interceptor, or the
// empty token for an interceptor built outside NewAuthInterceptor.
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
		authHeader := fmt.Sprintf("%s %s", types.AuthSchemeBearer, i.loadToken())
		if projects.HasProject(ctx) {
			project := projects.From(ctx)
			authHeader = fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, project.SecretKey)
		}

		req.Header().Add(types.AuthorizationKey, authHeader)
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

		authHeader := fmt.Sprintf("%s %s", types.AuthSchemeBearer, i.loadToken())
		if projects.HasProject(ctx) {
			project := projects.From(ctx)
			authHeader = fmt.Sprintf("%s %s", types.AuthSchemeAPIKey, project.SecretKey)
		}

		conn.RequestHeader().Add(types.AuthorizationKey, authHeader)
		conn.RequestHeader().Add(types.UserAgentKey, types.GoSDKType+"/"+version.Version)

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
