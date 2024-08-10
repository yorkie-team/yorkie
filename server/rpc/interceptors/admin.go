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
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/server/users"
)

// ErrUnauthenticated is returned when authentication is failed.
var ErrUnauthenticated = errors.New("authorization is not provided")

func isAdminService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.AdminService")
}

func isRequiredAuth(method string) bool {
	return method != "/yorkie.v1.AdminService/LogIn" &&
		method != "/yorkie.v1.AdminService/SignUp" &&
		method != "/yorkie.v1.AdminService/ChangePassword" &&
		method != "/yorkie.v1.AdminService/DeleteAccount"
}

// AdminServiceInterceptor is an interceptor for building additional context
// and handling authentication for AdminService.
type AdminServiceInterceptor struct {
	backend      *backend.Backend
	requestID    *requestID
	tokenManager *auth.TokenManager
}

// NewAdminServiceInterceptor creates a new instance of AdminServiceInterceptor.
func NewAdminServiceInterceptor(be *backend.Backend, tokenManager *auth.TokenManager) *AdminServiceInterceptor {
	return &AdminServiceInterceptor{
		backend:      be,
		requestID:    newRequestID("a"),
		tokenManager: tokenManager,
	}
}

// WrapUnary creates a unary server interceptor for authentication.
func (i *AdminServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		if !isAdminService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		ctx, err := i.buildContext(ctx, req.Spec().Procedure, req.Header())
		if err != nil {
			return nil, err
		}

		res, err := next(ctx, req)

		// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(req.Header())
		i.backend.Metrics.AddUserAgentWithEmptyProject(
			i.backend.Config.Hostname,
			sdkType,
			sdkVersion,
			req.Spec().Procedure,
		)

		if split := strings.Split(req.Spec().Procedure, "/"); len(split) == 3 {
			i.backend.Metrics.AddServerHandledCounter(
				"unary",
				split[1],
				split[2],
				connecthelper.ToRPCCodeString(err),
			)
		}

		return res, err
	}
}

// WrapStreamingClient creates a stream client interceptor for authentication.
func (i *AdminServiceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for authentication.
func (i *AdminServiceInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isAdminService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		ctx, err := i.buildContext(ctx, conn.Spec().Procedure, conn.RequestHeader())
		if err != nil {
			return err
		}

		err = next(ctx, conn)

		// TODO(hackerwins, emplam27): Consider splitting between admin and sdk metrics.
		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(conn.RequestHeader())
		i.backend.Metrics.AddUserAgentWithEmptyProject(
			i.backend.Config.Hostname,
			sdkType,
			sdkVersion,
			conn.Spec().Procedure,
		)

		if split := strings.Split(conn.Spec().Procedure, "/"); len(split) == 3 {
			i.backend.Metrics.AddServerHandledCounter(
				"server_stream",
				split[1],
				split[2],
				connecthelper.ToRPCCodeString(err),
			)
		}

		return err
	}
}

// buildContext builds a new context with the given request header.
func (i *AdminServiceInterceptor) buildContext(
	ctx context.Context,
	procedure string,
	header http.Header,
) (context.Context, error) {
	if isRequiredAuth(procedure) {
		user, err := i.authenticate(ctx, header)
		if err != nil {
			return nil, err
		}
		ctx = users.With(ctx, user)
	}

	ctx = logging.With(ctx, logging.New(i.requestID.next()))

	return ctx, nil
}

// authenticate does authenticate the request.
func (i *AdminServiceInterceptor) authenticate(
	ctx context.Context,
	header http.Header,
) (*types.User, error) {
	authorization := header.Get(types.AuthorizationKey)
	if authorization == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
	}

	// NOTE(raararaara): If the token is access token, return the user of the token.
	// This is used for the case where the user uses dashboard or CLI.
	claims, err := i.tokenManager.Verify(authorization)
	if err == nil {
		user, err := users.GetUserByName(ctx, i.backend, claims.Username)
		if err == nil {
			return user, nil
		}
	}

	// NOTE(raararaara): If the token is secret key, return the owner of the project.
	// This is used for the case where the user uses REST API.
	// TODO(hackerwins): In this case, attacker can hijack the project owner's identity.
	// We need to separate project-wide API and user-wide API from AdminService.
	project, err := projects.GetProjectFromSecretKey(ctx, i.backend, authorization)
	if err == nil {
		user, err := users.GetUserByID(ctx, i.backend, project.Owner)
		if err == nil {
			return user, nil
		}
	}

	return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
}
