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

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

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

// Unary creates a unary server interceptor for authorization.
func (i *AuthInterceptor) Unary() grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req,
		reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
			types.APIKeyKey, i.apiKey,
			types.AuthorizationKey, i.token,
			types.UserAgentKey, types.GoSDKType+"/"+version.Version,
		))
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// Stream creates a stream server interceptor for authorization.
func (i *AuthInterceptor) Stream() grpc.StreamClientInterceptor {
	return func(
		ctx context.Context,
		desc *grpc.StreamDesc,
		cc *grpc.ClientConn,
		method string,
		streamer grpc.Streamer,
		opts ...grpc.CallOption,
	) (grpc.ClientStream, error) {
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs(
			types.APIKeyKey, i.apiKey,
			types.AuthorizationKey, i.token,
			types.UserAgentKey, types.GoSDKType+"/"+version.Version,
		))
		return streamer(ctx, desc, cc, method, opts...)
	}
}
