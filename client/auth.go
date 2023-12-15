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

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/internal/version"
)

// AuthInterceptor is an interceptor for authentication.
type AuthInterceptor struct {
	apiKey string
	token  string
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(apiKey, token string) *AuthInterceptor {
	return &AuthInterceptor{
		apiKey: apiKey,
		token:  token,
	}
}

// WrapUnary creates a unary server interceptor for authorization.
func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		req.Header().Add(types.APIKeyKey, i.apiKey)
		req.Header().Add(types.AuthorizationKey, i.token)
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
		conn.RequestHeader().Set(types.AuthorizationKey, i.token)
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
