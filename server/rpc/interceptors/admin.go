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
	"fmt"
	"net/http"
	"strings"
	gotime "time"

	"connectrpc.com/connect"

	"github.com/yorkie-team/yorkie/api/types"
	"github.com/yorkie-team/yorkie/server/backend"
	"github.com/yorkie-team/yorkie/server/logging"
	"github.com/yorkie-team/yorkie/server/projects"
	"github.com/yorkie-team/yorkie/server/rpc/auth"
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/server/users"
)

var (
	// ErrUnauthenticated is returned when authentication is failed.
	ErrUnauthenticated = errors.New("authorization is not provided")

	// ErrInvalidAuthHeaderFormat is returned when the authorization header format is invalid.
	ErrInvalidAuthHeaderFormat = errors.New("invalid authorization header format")
)

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

		start := gotime.Now()
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
			code := connecthelper.ToRPCCodeString(err)
			i.backend.Metrics.AddServerHandledCounter("unary", split[1], split[2], code)
			i.backend.Metrics.ObserveServerHandledResponseSeconds(
				"unary",
				split[1],
				split[2],
				code,
				gotime.Since(start).Seconds(),
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
		newContext, err := i.authenticate(ctx, header)
		if err != nil {
			return nil, err
		}
		ctx = newContext
	}

	ctx = logging.With(ctx, logging.New(i.requestID.next()))

	return ctx, nil
}

// authenticate does authenticate the request.
func (i *AdminServiceInterceptor) authenticate(
	ctx context.Context,
	header http.Header,
) (context.Context, error) {
	// NOTE(hackerwins): The token can be provided by the Authorization header or cookie.
	authHeader := header.Get(types.AuthorizationKey)
	if authHeader == "" {
		cookie, err := (&http.Request{Header: header}).Cookie(types.SessionKey)
		if err != nil && err != http.ErrNoCookie {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if cookie != nil {
			authHeader = fmt.Sprintf("%s %s", types.AuthSchemeBearer, cookie.Value)
		}
	}
	if authHeader == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
	}

	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 {
		return nil, connect.NewError(connect.CodeUnauthenticated, ErrInvalidAuthHeaderFormat)
	}
	scheme := parts[0]
	param := parts[1]

	switch {
	case strings.EqualFold(scheme, types.AuthSchemeBearer):
		// If the scheme is Bearer, verify the token and retrieve the user.
		claims, err := i.tokenManager.Verify(param)
		if err == nil {
			user, err := users.GetUserByName(ctx, i.backend, claims.Username)
			if err == nil {
				ctx = users.With(ctx, user)
				return ctx, nil
			}
		}
	case strings.EqualFold(scheme, types.AuthSchemeAPIKey):
		// If the scheme is API-Key, verify the secret key and retrieve the project.
		project, err := projects.ProjectFromSecretKey(ctx, i.backend, param)
		if err == nil {
			ctx = projects.With(ctx, project)
			return ctx, nil
		}
	}

	return nil, connect.NewError(connect.CodeUnauthenticated, ErrUnauthenticated)
}
