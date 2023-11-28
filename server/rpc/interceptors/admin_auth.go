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
	"connectrpc.com/connect"
	"context"
	"net/http"
	"strings"

	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/users"
)

// AdminAuthInterceptor is an interceptor for authentication.
type AdminAuthInterceptor struct {
	backend      *backend.Backend
	tokenManager *auth.TokenManager
}

// NewAdminAuthInterceptor creates a new instance of AdminAuthInterceptor.
func NewAdminAuthInterceptor(be *backend.Backend, tokenManager *auth.TokenManager) *AdminAuthInterceptor {
	return &AdminAuthInterceptor{
		backend:      be,
		tokenManager: tokenManager,
	}
}

// WrapUnary creates a unary server interceptor for authentication.
func (i *AdminAuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isAdminService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		if isRequiredAuth(req.Spec().Procedure) {
			user, err := i.authenticate(ctx, req.Header())
			if err != nil {
				return nil, err
			}
			ctx = users.With(ctx, user)
		}

		return next(ctx, req)
	}
}

// WrapStreamingClient creates a stream client interceptor for building additional context.
func (i *AdminAuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		conn := next(ctx, spec)
		return conn
	}
}

// WrapStreamingHandler creates a stream server interceptor for building additional context.
func (i *AdminAuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isAdminService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		if isRequiredAuth(conn.Spec().Procedure) {
			user, err := i.authenticate(ctx, conn.RequestHeader())
			if err != nil {
				return err
			}
			ctx = users.With(ctx, user)
		}

		return next(ctx, conn)
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
func (i *AdminAuthInterceptor) authenticate(
	ctx context.Context,
	header http.Header,
) (*types.User, error) {
	authorization := header.Get(types.AuthorizationKey)
	if len(authorization) == 0 {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is not provided")
	}

	claims, err := i.tokenManager.Verify(authorization)
	if err != nil {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is invalid")
	}

	user, err := users.GetUser(ctx, i.backend, claims.Username)
	if err != nil {
		return nil, grpcstatus.Errorf(codes.Unauthenticated, "authorization is invalid")
	}

	return user, nil
}
