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

// Package interceptors provides the interceptors for RPC.
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
	"github.com/yorkie-team/yorkie/server/rpc/connecthelper"
	"github.com/yorkie-team/yorkie/server/rpc/metadata"
)

func isYorkieService(method string) bool {
	return strings.HasPrefix(method, "/yorkie.v1.YorkieService/")
}

// YorkieServiceInterceptor is an interceptor for building additional context
// and handling authentication for YorkieService.
type YorkieServiceInterceptor struct {
	backend   *backend.Backend
	requestID *requestID
}

// NewYorkieServiceInterceptor creates a new instance of YorkieServiceInterceptor.
func NewYorkieServiceInterceptor(be *backend.Backend) *YorkieServiceInterceptor {
	return &YorkieServiceInterceptor{
		backend:   be,
		requestID: newRequestID("r"),
	}
}

// WrapUnary creates a unary server interceptor for building additional context.
func (i *YorkieServiceInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(
		ctx context.Context,
		req connect.AnyRequest,
	) (connect.AnyResponse, error) {
		start := gotime.Now()
		if !isYorkieService(req.Spec().Procedure) {
			return next(ctx, req)
		}

		ctx, err := i.buildContext(ctx, req.Header())
		if err != nil {
			return nil, err
		}

		res, err := next(ctx, req)

		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(req.Header())
		i.backend.Metrics.AddUserAgent(
			i.backend.Config.Hostname,
			projects.From(ctx),
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
			i.backend.Metrics.ObserveServerHandledResponseSeconds(
				"unary",
				split[1],
				split[2],
				connecthelper.ToRPCCodeString(err),
				gotime.Since(start).Seconds(),
			)
		}

		return res, err
	}
}

// WrapStreamingClient creates a stream client interceptor for building additional context.
func (i *YorkieServiceInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(
		ctx context.Context,
		spec connect.Spec,
	) connect.StreamingClientConn {
		return next(ctx, spec)
	}
}

// WrapStreamingHandler creates a stream server interceptor for building additional context.
func (i *YorkieServiceInterceptor) WrapStreamingHandler(
	next connect.StreamingHandlerFunc,
) connect.StreamingHandlerFunc {
	return func(
		ctx context.Context,
		conn connect.StreamingHandlerConn,
	) error {
		if !isYorkieService(conn.Spec().Procedure) {
			return next(ctx, conn)
		}

		ctx, err := i.buildContext(ctx, conn.RequestHeader())
		if err != nil {
			return err
		}

		err = next(ctx, conn)

		sdkType, sdkVersion := connecthelper.SDKTypeAndVersion(conn.RequestHeader())
		i.backend.Metrics.AddUserAgent(
			i.backend.Config.Hostname,
			projects.From(ctx),
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

// buildContext builds a context data for RPC. It includes the metadata of the
// request and the project information.
func (i *YorkieServiceInterceptor) buildContext(ctx context.Context, header http.Header) (context.Context, error) {
	// 01. building metadata
	md := metadata.Metadata{}

	apiKey := header.Get(types.APIKeyKey)
	if apiKey == "" && !i.backend.Config.UseDefaultProject {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("api key is not provided"))
	}
	if apiKey != "" {
		md.APIKey = apiKey
	}

	if authorization := header.Get(types.AuthorizationKey); authorization != "" {
		md.Authorization = authorization
	}
	ctx = metadata.With(ctx, md)
	cacheKey := md.APIKey

	// 02. Build Project from API Key
	if _, ok := i.backend.Cache.Project.Get(cacheKey); !ok {
		prj, err := projects.GetProjectFromAPIKey(ctx, i.backend, md.APIKey)
		if err != nil {
			return nil, connecthelper.ToStatusError(err)
		}
		i.backend.Cache.Project.Add(cacheKey, prj)
	}
	project, _ := i.backend.Cache.Project.Get(cacheKey)
	ctx = projects.With(ctx, project)

	// 03. Check CORS after project is loaded
	if err := i.checkCORS(ctx, header); err != nil {
		return nil, err
	}

	// 04. Build Logger with request ID
	ctx = logging.With(ctx, logging.New(i.requestID.next()))

	return ctx, nil
}

func (i *YorkieServiceInterceptor) checkCORS(ctx context.Context, header http.Header) error {
	// NOTE(hackerwins): Check if the request is from a browser
	userAgent := header.Get("User-Agent")
	isBrowser := strings.Contains(strings.ToLower(userAgent), "mozilla") ||
		strings.Contains(strings.ToLower(userAgent), "chrome") ||
		strings.Contains(strings.ToLower(userAgent), "safari") ||
		strings.Contains(strings.ToLower(userAgent), "edge") ||
		strings.Contains(strings.ToLower(userAgent), "firefox")
	if !isBrowser {
		return nil
	}

	origin := header.Get("Origin")

	// Not a CORS request
	if origin == "" {
		return nil
	}

	project := projects.From(ctx)
	if project == nil {
		return nil
	}

	if len(project.AllowedOrigins) == 0 {
		return nil
	}

	for _, allowed := range project.AllowedOrigins {
		if allowed == "*" || allowed == origin {
			return nil
		}
	}

	return connect.NewError(
		connect.CodePermissionDenied,
		fmt.Errorf("origin %q not allowed", origin),
	)
}
