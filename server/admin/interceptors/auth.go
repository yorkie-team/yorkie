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

package interceptors

import (
	"context"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/server/admin/auth"
)

// AuthInterceptor is an interceptor for authentication.
type AuthInterceptor struct {
	tokenManager *auth.TokenManager
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(tokenManager *auth.TokenManager) *AuthInterceptor {
	return &AuthInterceptor{
		tokenManager: tokenManager,
	}
}

// Unary creates a unary server interceptor for authentication.
func (i *AuthInterceptor) Unary() grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req interface{},
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (resp interface{}, err error) {
		if err := i.authenticate(ctx, info.FullMethod); err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

// Stream creates a stream server interceptor for authentication.
func (i *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		if err := i.authenticate(stream.Context(), info.FullMethod); err != nil {
			return err
		}
		return handler(srv, stream)
	}
}

// authenticate does authenticate the request.
func (i *AuthInterceptor) authenticate(
	ctx context.Context,
	method string,
) error {
	// NOTE(hackerwins): We don't need to authenticate the request if the
	// request is from the peer clusters.
	if method == "/api.Admin/LogIn" ||
		!strings.HasPrefix(method, "/api.Admin/") {
		return nil
	}

	data, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok {
		return grpcstatus.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	authorization := data["authorization"]
	if len(authorization) == 0 {
		return grpcstatus.Errorf(codes.Unauthenticated, "authorization is not provided")
	}

	// TODO: store the userClaims in the `ctx`.
	if _, err := i.tokenManager.Verify(authorization[0]); err != nil {
		return grpcstatus.Errorf(codes.Unauthenticated, "authorization is invalid")
	}

	return nil
}
