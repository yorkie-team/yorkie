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

	grpcmiddleware "github.com/grpc-ecosystem/go-grpc-middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/grpchelper"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/users"
)

// AuthInterceptor is an interceptor for authentication.
type AuthInterceptor struct {
	backend      *backend.Backend
	tokenManager *auth.TokenManager
}

// NewAuthInterceptor creates a new instance of AuthInterceptor.
func NewAuthInterceptor(be *backend.Backend, tokenManager *auth.TokenManager) *AuthInterceptor {
	return &AuthInterceptor{
		backend:      be,
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
		if isAdminService(info.FullMethod) && isRequiredAuth(info.FullMethod) {
			user, err := i.authenticate(ctx, info.FullMethod)
			if err != nil {
				return nil, err
			}
			ctx = users.With(ctx, user)
		}

		resp, err = handler(ctx, req)

		if isAdminService(info.FullMethod) {
			// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
			data, ok := grpcmetadata.FromIncomingContext(ctx)
			if ok {
				sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
				i.backend.Metrics.AddUserAgentWithEmptyProject(
					i.backend.Config.Hostname,
					sdkType,
					sdkVersion,
					info.FullMethod,
				)
			}
		}

		return resp, err
	}
}

// Stream creates a stream server interceptor for authentication.
func (i *AuthInterceptor) Stream() grpc.StreamServerInterceptor {
	return func(
		srv interface{},
		stream grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) (err error) {
		ctx := stream.Context()
		if isAdminService(info.FullMethod) && isRequiredAuth(info.FullMethod) {
			user, err := i.authenticate(ctx, info.FullMethod)
			if err != nil {
				return err
			}

			wrapped := grpcmiddleware.WrapServerStream(stream)
			wrapped.WrappedContext = users.With(ctx, user)
			stream = wrapped
		}

		err = handler(srv, stream)

		if isAdminService(info.FullMethod) {
			// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
			data, ok := grpcmetadata.FromIncomingContext(ctx)
			if ok {
				sdkType, sdkVersion := grpchelper.SDKTypeAndVersion(data)
				i.backend.Metrics.AddUserAgentWithEmptyProject(
					i.backend.Config.Hostname,
					sdkType,
					sdkVersion,
					info.FullMethod,
				)
			}
		}

		return err
	}
}

func isAdminService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.AdminService")
}

func isRequiredAuth(method string) bool {
	return method != "/yorkie.v1.AdminService/LogIn" &&
		method != "/yorkie.v1.AdminService/SignUp"
}

// authenticate does authenticate the request.
func (i *AuthInterceptor) authenticate(
	ctx context.Context,
	method string,
) (*types.User, error) {
	if !isRequiredAuth(method) {
		return nil, nil
	}

	data, ok := grpcmetadata.FromIncomingContext(ctx)
	if !ok {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "metadata is not provided")
	}

	authorization := data[types.AuthorizationKey]
	if len(authorization) == 0 {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is not provided")
	}

	claims, err := i.tokenManager.Verify(authorization[0])
	if err != nil {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is invalid")
	}

	user, err := users.GetUser(ctx, i.backend, claims.Username)
	if err != nil {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is invalid")
	}

	return user, nil
}
